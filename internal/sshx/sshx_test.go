package sshx

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/billzhuang/wisp/internal/testutil/sshserver"
	"github.com/billzhuang/wisp/internal/transport"
	"golang.org/x/crypto/ssh"
)

func dialCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func TestDialExecRoundTrip(t *testing.T) {
	srv, err := sshserver.Start(func(stdin io.Reader, stdout io.Writer, cmd string, pty sshserver.PTYRequest) {
		io.WriteString(stdout, "ran: "+cmd)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctx, cancel := dialCtx(t)
	defer cancel()

	sess, err := Dial(ctx, transport.NewNetDialer(), Config{
		Addr:    srv.Addr(),
		User:    "tester",
		Auth:    []ssh.AuthMethod{ssh.Password("hunter2")},
		HostKey: InsecureIgnoreHostKey(),
		Cols:    80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer sess.Close()

	if err := sess.Run("echo hi"); err != nil {
		t.Fatalf("run: %v", err)
	}
	out, _ := io.ReadAll(sess.Stdout)
	if string(out) != "ran: echo hi" {
		t.Fatalf("output = %q", out)
	}
}

func TestPTYRequestParameters(t *testing.T) {
	got := make(chan sshserver.PTYRequest, 1)
	srv, err := sshserver.Start(func(stdin io.Reader, stdout io.Writer, cmd string, pty sshserver.PTYRequest) {
		got <- pty
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctx, cancel := dialCtx(t)
	defer cancel()

	sess, err := Dial(ctx, transport.NewNetDialer(), Config{
		Addr: srv.Addr(), User: "u",
		Auth:    []ssh.AuthMethod{ssh.Password("")},
		HostKey: InsecureIgnoreHostKey(),
		Term:    "xterm-256color", Cols: 120, Rows: 40,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}

	select {
	case pty := <-got:
		if pty.Term != "xterm-256color" || pty.Cols != 120 || pty.Rows != 40 {
			t.Fatalf("pty = %+v, want term=xterm-256color 120x40", pty)
		}
	case <-ctx.Done():
		t.Fatal("timeout waiting for pty request")
	}
}

func TestResizeSendsWindowChange(t *testing.T) {
	srv, err := sshserver.Start(func(stdin io.Reader, stdout io.Writer, cmd string, pty sshserver.PTYRequest) {
		io.ReadAll(stdin) // keep session open until client closes
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctx, cancel := dialCtx(t)
	defer cancel()

	sess, err := Dial(ctx, transport.NewNetDialer(), Config{
		Addr: srv.Addr(), User: "u",
		Auth:    []ssh.AuthMethod{ssh.Password("")},
		HostKey: InsecureIgnoreHostKey(),
		Cols:    80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Shell(); err != nil {
		t.Fatal(err)
	}
	if err := sess.Resize(132, 50); err != nil {
		t.Fatalf("resize: %v", err)
	}
	sess.Close()

	// Give the server a moment to record the event.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rs := srv.Resizes()
		if len(rs) == 1 && rs[0].Cols == 132 && rs[0].Rows == 50 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("window-change not observed: %+v", srv.Resizes())
}

func TestPasswordAuthRejected(t *testing.T) {
	srv, err := sshserver.Start(
		func(io.Reader, io.Writer, string, sshserver.PTYRequest) {},
		sshserver.WithPassword("correct"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctx, cancel := dialCtx(t)
	defer cancel()

	_, err = Dial(ctx, transport.NewNetDialer(), Config{
		Addr: srv.Addr(), User: "u",
		Auth:    []ssh.AuthMethod{ssh.Password("wrong")},
		HostKey: InsecureIgnoreHostKey(),
	})
	if err == nil {
		t.Fatal("expected auth failure")
	}
}

func TestHostKeyRequired(t *testing.T) {
	_, err := Dial(context.Background(), transport.NewNetDialer(), Config{
		Addr: "127.0.0.1:1", User: "u",
	})
	if err == nil || !strings.Contains(err.Error(), "HostKey") {
		t.Fatalf("expected HostKey required error, got %v", err)
	}
}

func TestKnownHostsTOFU(t *testing.T) {
	srv, err := sshserver.Start(func(_ io.Reader, stdout io.Writer, cmd string, _ sshserver.PTYRequest) {
		io.WriteString(stdout, "ok")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	khPath := filepath.Join(t.TempDir(), "known_hosts")
	cb, err := KnownHostsCallback(khPath, true)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := dialCtx(t)
	defer cancel()

	sess, err := Dial(ctx, transport.NewNetDialer(), Config{
		Addr: srv.Addr(), User: "u",
		Auth:    []ssh.AuthMethod{ssh.Password("")},
		HostKey: cb,
	})
	if err != nil {
		t.Fatalf("first dial (TOFU): %v", err)
	}
	sess.Run("x")
	io.ReadAll(sess.Stdout)
	sess.Close()

	// The host key should now be recorded.
	data, err := os.ReadFile(khPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		t.Fatal("known_hosts not written on first use")
	}

	// A second dial against the same (recorded) key must succeed without TOFU.
	cb2, err := KnownHostsCallback(khPath, false)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := dialCtx(t)
	defer cancel2()
	sess2, err := Dial(ctx2, transport.NewNetDialer(), Config{
		Addr: srv.Addr(), User: "u",
		Auth:    []ssh.AuthMethod{ssh.Password("")},
		HostKey: cb2,
	})
	if err != nil {
		t.Fatalf("second dial against recorded key: %v", err)
	}
	sess2.Close()
}
