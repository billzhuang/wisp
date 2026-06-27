package app_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/billzhuang/wisp/internal/app"
	"github.com/billzhuang/wisp/internal/render"
	"github.com/billzhuang/wisp/internal/sshx"
	"github.com/billzhuang/wisp/internal/terminal"
	"github.com/billzhuang/wisp/internal/testutil/sshserver"
	"github.com/billzhuang/wisp/internal/transport"
	"golang.org/x/crypto/ssh"
)

// TestEndToEndSeam exercises the entire wisp pipeline end-to-end with no real
// tailnet and no GPU: NetDialer -> SSH handshake -> PTY -> remote output ->
// terminal engine -> rendered grid. This is the integration counterpart to the
// per-layer unit tests: it proves the layers compose.
func TestEndToEndSeam(t *testing.T) {
	// Fake remote program emits colored, cursor-addressed output a real shell
	// might produce, then exits.
	srv, err := sshserver.Start(func(stdin io.Reader, stdout io.Writer, cmd string, pty sshserver.PTYRequest) {
		// Clear screen, home, print a banner, then a colored second line.
		fmt.Fprint(stdout, "\x1b[2J\x1b[H")
		fmt.Fprintf(stdout, "wisp on %s %dx%d\r\n", pty.Term, pty.Cols, pty.Rows)
		fmt.Fprint(stdout, "\x1b[32mgreen\x1b[0m line\r\n")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctrl, err := app.Dial(ctx, transport.NewNetDialer(), sshx.Config{
		Addr:    srv.Addr(),
		User:    "tester",
		Auth:    []ssh.AuthMethod{ssh.Password("")},
		HostKey: sshx.InsecureIgnoreHostKey(),
		Term:    "xterm-256color",
		Cols:    40, Rows: 10,
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ctrl.Close()

	if err := ctrl.Start(); err != nil {
		t.Fatalf("start shell: %v", err)
	}

	eng := terminal.NewTerminal(terminal.WithSize(40, 10))
	// Headless frontend pumps remote output through the engine until EOF.
	if err := (render.Headless{}).Run(ctx, ctrl, eng); err != nil {
		t.Fatalf("frontend run: %v", err)
	}

	g := eng.Snapshot()
	if got := g.Line(0); got != "wisp on xterm-256color 40x10" {
		t.Fatalf("line 0 = %q", got)
	}
	if got := g.Line(1); got != "green line" {
		t.Fatalf("line 1 = %q", got)
	}
	// The word "green" carried SGR green; verify the engine applied it.
	cell := g.At(0, 1)
	if cell.Rune != 'g' || cell.FG == nil {
		t.Fatalf("expected colored 'g' at (0,1), got %+v", cell)
	}
}

// TestInputForwarding proves keystrokes flow client -> remote stdin and the
// echoed result lands back in the engine grid.
func TestInputForwarding(t *testing.T) {
	srv, err := sshserver.Start(func(stdin io.Reader, stdout io.Writer, cmd string, pty sshserver.PTYRequest) {
		// Echo one line of input back, as a PTY in cooked mode would.
		buf := make([]byte, 64)
		n, _ := stdin.Read(buf)
		fmt.Fprintf(stdout, "echo:%s", strings.TrimSpace(string(buf[:n])))
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ctrl, err := app.Dial(ctx, transport.NewNetDialer(), sshx.Config{
		Addr:    srv.Addr(),
		User:    "u",
		Auth:    []ssh.AuthMethod{ssh.Password("")},
		HostKey: sshx.InsecureIgnoreHostKey(),
		Cols:    40, Rows: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ctrl.Close()
	if err := ctrl.Start(); err != nil {
		t.Fatal(err)
	}

	if err := ctrl.Input([]byte("hello\n")); err != nil {
		t.Fatalf("input: %v", err)
	}

	eng := terminal.NewTerminal(terminal.WithSize(40, 5))
	if err := (render.Headless{}).Run(ctx, ctrl, eng); err != nil {
		t.Fatal(err)
	}
	if got := eng.Snapshot().Line(0); got != "echo:hello" {
		t.Fatalf("round-trip line = %q, want echo:hello", got)
	}
}
