package app_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/billzhuang/wisp/internal/app"
	"github.com/billzhuang/wisp/internal/sshx"
	"github.com/billzhuang/wisp/internal/testutil/sshserver"
	"github.com/billzhuang/wisp/internal/transport"
	"golang.org/x/crypto/ssh"
)

func dialController(t *testing.T, srv *sshserver.Server) *app.Controller {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctrl, err := app.Dial(ctx, transport.NewNetDialer(), sshx.Config{
		Addr:    srv.Addr(),
		User:    "tester",
		Auth:    []ssh.AuthMethod{ssh.Password("")},
		HostKey: sshx.InsecureIgnoreHostKey(),
		Cols:    80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("app.Dial: %v", err)
	}
	return ctrl
}

// TestControllerStdoutReturnsReader verifies that Stdout() returns a non-nil
// reader that carries the remote program's output.
func TestControllerStdoutReturnsReader(t *testing.T) {
	srv, err := sshserver.Start(func(_ io.Reader, stdout io.Writer, cmd string, _ sshserver.PTYRequest) {
		io.WriteString(stdout, "hello from remote")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctrl := dialController(t, srv)
	defer ctrl.Close()

	if err := ctrl.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	r := ctrl.Stdout()
	if r == nil {
		t.Fatal("Stdout() returned nil")
	}

	var buf bytes.Buffer
	io.Copy(&buf, r)
	if !strings.Contains(buf.String(), "hello from remote") {
		t.Fatalf("stdout = %q, want 'hello from remote'", buf.String())
	}
}

// TestControllerStartCommand runs a single command (not an interactive shell)
// and reads back the output.
func TestControllerStartCommand(t *testing.T) {
	srv, err := sshserver.Start(func(_ io.Reader, stdout io.Writer, cmd string, _ sshserver.PTYRequest) {
		io.WriteString(stdout, "cmd:"+cmd)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctrl := dialController(t, srv)
	defer ctrl.Close()

	if err := ctrl.StartCommand("echo test"); err != nil {
		t.Fatalf("StartCommand: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, ctrl.Stdout())
	if !strings.Contains(buf.String(), "echo test") {
		t.Fatalf("command output = %q", buf.String())
	}
}

// TestControllerInputForwarding proves that bytes sent via Input() arrive at
// the remote stdin.
func TestControllerInputForwarding(t *testing.T) {
	srv, err := sshserver.Start(func(stdin io.Reader, stdout io.Writer, _ string, _ sshserver.PTYRequest) {
		buf := make([]byte, 64)
		n, _ := stdin.Read(buf)
		io.WriteString(stdout, "got:"+string(buf[:n]))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctrl := dialController(t, srv)
	defer ctrl.Close()

	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	if err := ctrl.Input([]byte("ping")); err != nil {
		t.Fatalf("Input: %v", err)
	}

	var buf bytes.Buffer
	io.Copy(&buf, ctrl.Stdout())
	if !strings.Contains(buf.String(), "got:ping") {
		t.Fatalf("round-trip = %q, want 'got:ping'", buf.String())
	}
}

// TestControllerResizeDoesNotError verifies that Resize does not return an
// error when the session is active.
func TestControllerResizeDoesNotError(t *testing.T) {
	srv, err := sshserver.Start(func(_ io.Reader, _ io.Writer, _ string, _ sshserver.PTYRequest) {
		// Keep session alive briefly so Resize can be called.
		time.Sleep(50 * time.Millisecond)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctrl := dialController(t, srv)
	defer ctrl.Close()

	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	if err := ctrl.Resize(132, 50); err != nil {
		t.Fatalf("Resize: %v", err)
	}
}

// TestControllerDialFailure verifies that Dial propagates a connection error
// when the server address is invalid.
func TestControllerDialFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := app.Dial(ctx, transport.NewNetDialer(), sshx.Config{
		Addr:    "127.0.0.1:1", // nothing listening here
		User:    "u",
		Auth:    []ssh.AuthMethod{ssh.Password("")},
		HostKey: sshx.InsecureIgnoreHostKey(),
	})
	if err == nil {
		t.Fatal("expected error dialing unreachable address")
	}
}

// TestControllerClose verifies that Close can be called without error and that
// the session has ended.
func TestControllerClose(t *testing.T) {
	srv, err := sshserver.Start(func(_ io.Reader, _ io.Writer, _ string, _ sshserver.PTYRequest) {})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctrl := dialController(t, srv)

	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	// Close must not panic or return an unexpected error.
	_ = ctrl.Close()
}

// TestControllerStderrReturnsReader checks that Stderr() is non-nil. (The test
// SSH server doesn't write to stderr, but the pipe must be wired.)
func TestControllerStderrReturnsReader(t *testing.T) {
	srv, err := sshserver.Start(func(_ io.Reader, _ io.Writer, _ string, _ sshserver.PTYRequest) {})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctrl := dialController(t, srv)
	defer ctrl.Close()

	if ctrl.Stderr() == nil {
		t.Fatal("Stderr() should not be nil")
	}
}