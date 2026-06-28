package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/billzhuang/wisp/internal/app"
	"github.com/billzhuang/wisp/internal/localpty"
	"github.com/billzhuang/wisp/internal/render"
	"github.com/billzhuang/wisp/internal/terminal"
)

// TestEndToEndSeam exercises the wisp render pipeline end-to-end with no tailnet
// and no GPU: a local shell on a PTY -> shell output -> terminal engine ->
// rendered grid. This is the integration counterpart to the per-layer unit
// tests: it proves the layers compose.
func TestEndToEndSeam(t *testing.T) {
	// A shell command emits colored, cursor-addressed output a real shell might
	// produce, then exits.
	const script = `printf '\033[2J\033[H'; printf 'wisp local terminal\r\n'; printf '\033[32mgreen\033[0m line\r\n'`

	ctrl := app.NewLocal(localpty.Config{
		Path: "/bin/sh",
		Args: []string{"/bin/sh", "-c", script},
		Term: "xterm-256color",
		Cols: 40, Rows: 10,
	})
	if err := ctrl.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ctrl.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	eng := terminal.NewTerminal(terminal.WithSize(40, 10))
	// Headless frontend pumps shell output through the engine until EOF.
	if err := (render.Headless{}).Run(ctx, ctrl, eng); err != nil {
		t.Fatalf("frontend run: %v", err)
	}

	g := eng.Snapshot()
	if got := g.Line(0); got != "wisp local terminal" {
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

// TestInputForwarding proves keystrokes flow client -> shell stdin and the
// echoed result lands back in the engine grid.
func TestInputForwarding(t *testing.T) {
	// `cat` echoes one line of input straight back.
	ctrl := app.NewLocal(localpty.Config{
		Path: "/bin/sh",
		Args: []string{"/bin/sh", "-c", "cat"},
		Cols: 40, Rows: 5,
	})
	if err := ctrl.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer ctrl.Close()

	if err := ctrl.Input([]byte("hello\n")); err != nil {
		t.Fatalf("input: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	eng := terminal.NewTerminal(terminal.WithSize(40, 5))
	// Observe the grid as output arrives; stop once the echo lands (cat keeps the
	// session open, so we cannot wait for EOF).
	done := make(chan struct{})
	frontendErr := make(chan error, 1)
	go func() {
		frontendErr <- (render.Headless{OnFrame: func(e terminal.Engine) {
			if e.Snapshot().Line(0) == "hello" {
				select {
				case <-done:
				default:
					close(done)
				}
			}
		}}).Run(ctx, ctrl, eng)
	}()

	select {
	case <-done:
		// success
	case <-ctx.Done():
		t.Fatalf("round-trip line = %q, want hello", eng.Snapshot().Line(0))
	}
}
