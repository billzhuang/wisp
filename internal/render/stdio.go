//go:build !ebiten

package render

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/billzhuang/wisp/internal/terminal"
	"golang.org/x/term"
)

// NewDefault returns the frontend used when the binary is built without the
// `ebiten` tag: a raw stdio passthrough. The local OS terminal does the actual
// glyph rendering, while wisp still feeds the shell's output through the
// terminal.Engine (so the engine — and any scrollback/inspection built on it —
// stays in the loop). This is a fully working terminal with no GUI dependency.
func NewDefault() Frontend { return &stdioFrontend{} }

type stdioFrontend struct{}

// Run puts the local terminal into raw mode, mirrors the shell's output to both
// the engine and os.Stdout, and pumps local stdin to the shell. It tracks
// SIGWINCH-style resizes via the local terminal size at startup; live resize
// handling is wired by the caller through ctrl.Resize when a signal arrives.
func (s *stdioFrontend) Run(ctx context.Context, ctrl Controller, eng terminal.Engine) error {
	fd := int(os.Stdin.Fd())
	var restore func()
	if term.IsTerminal(fd) {
		old, err := term.MakeRaw(fd)
		if err == nil {
			restore = func() {
				if rerr := term.Restore(fd, old); rerr != nil {
					fmt.Fprintf(os.Stderr, "wisp: failed to restore terminal: %v\n", rerr)
				}
			}
		}
		if w, h, err := term.GetSize(fd); err == nil {
			eng.Resize(w, h)
			ctrl.Resize(w, h)
		}
	}
	if restore != nil {
		defer restore()
	}

	// Local stdin -> shell.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				if werr := ctrl.Input(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Shell -> engine + local stdout.
	out := io.MultiWriter(eng, os.Stdout)
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(out, ctrl.Stdout())
		done <- err
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if err == io.EOF {
			return nil
		}
		return err
	}
}
