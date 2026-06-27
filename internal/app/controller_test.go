package app_test

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/billzhuang/wisp/internal/app"
	"github.com/billzhuang/wisp/internal/sshx"
	"github.com/billzhuang/wisp/internal/testutil/sshserver"
	"github.com/billzhuang/wisp/internal/transport"
	"golang.org/x/crypto/ssh"
)

func dialController(t *testing.T, h sshserver.Handler, cols, rows int) (*app.Controller, *sshserver.Server) {
	t.Helper()
	srv, err := sshserver.Start(h)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	ctrl, err := app.Dial(ctx, transport.NewNetDialer(), sshx.Config{
		Addr:    srv.Addr(),
		User:    "u",
		Auth:    []ssh.AuthMethod{ssh.Password("")},
		HostKey: sshx.InsecureIgnoreHostKey(),
		Cols:    cols, Rows: rows,
	})
	if err != nil {
		srv.Close()
		t.Fatalf("dial: %v", err)
	}
	// Cleanup runs LIFO: the client (ctrl) must close before the server, since
	// sshserver.Close waits for its connection goroutines to drain — which only
	// happens once the client disconnects. Register srv.Close first so it runs
	// last.
	t.Cleanup(func() { srv.Close() })
	t.Cleanup(func() { ctrl.Close() })
	return ctrl, srv
}

func TestControllerStartCommandAndWait(t *testing.T) {
	ctrl, _ := dialController(t, func(_ io.Reader, stdout io.Writer, cmd string, _ sshserver.PTYRequest) {
		fmt.Fprintf(stdout, "cmd=%s", cmd)
	}, 40, 10)

	if err := ctrl.StartCommand("uptime"); err != nil {
		t.Fatalf("start command: %v", err)
	}
	out, err := io.ReadAll(ctrl.Stdout())
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if string(out) != "cmd=uptime" {
		t.Fatalf("stdout = %q", out)
	}
	if err := ctrl.Wait(); err != nil {
		t.Fatalf("wait (clean exit expected): %v", err)
	}
}

func TestControllerResizePropagates(t *testing.T) {
	ctrl, srv := dialController(t, func(stdin io.Reader, _ io.Writer, _ string, _ sshserver.PTYRequest) {
		io.ReadAll(stdin) // keep session open
	}, 80, 24)
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Resize(100, 30); err != nil {
		t.Fatalf("resize: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rs := srv.Resizes()
		if len(rs) == 1 && rs[0].Cols == 100 && rs[0].Rows == 30 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("resize not propagated to remote PTY: %+v", srv.Resizes())
}

func TestControllerInputReachesRemote(t *testing.T) {
	got := make(chan string, 1)
	ctrl, _ := dialController(t, func(stdin io.Reader, _ io.Writer, _ string, _ sshserver.PTYRequest) {
		buf := make([]byte, 16)
		n, _ := stdin.Read(buf)
		got <- string(buf[:n])
	}, 40, 10)
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Input([]byte("hi")); err != nil {
		t.Fatalf("input: %v", err)
	}
	select {
	case s := <-got:
		if s != "hi" {
			t.Fatalf("remote received %q, want hi", s)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("remote never received input")
	}
}

// TestControllerDialFailure verifies app.Dial propagates a connection error
// when nothing is listening at the address.
func TestControllerDialFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := app.Dial(ctx, transport.NewNetDialer(), sshx.Config{
		Addr:    "127.0.0.1:1", // nothing listening
		User:    "u",
		Auth:    []ssh.AuthMethod{ssh.Password("")},
		HostKey: sshx.InsecureIgnoreHostKey(),
	})
	if err == nil {
		t.Fatal("expected error dialing unreachable address")
	}
}

// TestControllerClose verifies Close can be called on an active session without
// returning an unexpected error.
func TestControllerClose(t *testing.T) {
	ctrl, _ := dialController(t, func(io.Reader, io.Writer, string, sshserver.PTYRequest) {}, 40, 10)
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Close(); err != nil {
		t.Fatalf("Close returned: %v", err)
	}
}

// TestControllerStderrReturnsReader checks the Stderr pipe is wired (non-nil).
func TestControllerStderrReturnsReader(t *testing.T) {
	ctrl, _ := dialController(t, func(io.Reader, io.Writer, string, sshserver.PTYRequest) {}, 40, 10)
	if ctrl.Stderr() == nil {
		t.Fatal("Stderr() should not be nil")
	}
}
