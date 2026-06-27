// Package app wires the network/SSH half of wisp into something a frontend can
// drive. A Controller owns a live SSH session: it exposes the remote output
// stream, forwards user input to the remote stdin, and propagates window
// resizes to both the remote PTY and (via the frontend) the terminal engine.
//
// The controller is deliberately ignorant of rendering. A render.Frontend
// consumes a Controller through the small render.Controller interface, so the
// same controller drives the headless test frontend, the stdio passthrough, or
// the Ebitengine GUI without change.
package app

import (
	"context"
	"io"

	"github.com/billzhuang/wisp/internal/sshx"
	"github.com/billzhuang/wisp/internal/transport"
)

// Controller manages one SSH session for a frontend.
type Controller struct {
	sess *sshx.Session
}

// Dial opens an SSH session through the dialer and returns a controller for it.
// The caller is responsible for Close.
func Dial(ctx context.Context, d transport.Dialer, cfg sshx.Config) (*Controller, error) {
	sess, err := sshx.Dial(ctx, d, cfg)
	if err != nil {
		return nil, err
	}
	return &Controller{sess: sess}, nil
}

// NewController wraps an already-established session (used by tests).
func NewController(sess *sshx.Session) *Controller { return &Controller{sess: sess} }

// Start launches the remote login shell.
func (c *Controller) Start() error { return c.sess.Shell() }

// StartCommand runs a single remote command instead of an interactive shell.
func (c *Controller) StartCommand(cmd string) error { return c.sess.Run(cmd) }

// Stdout is the combined remote output stream. A frontend copies this into its
// terminal engine.
func (c *Controller) Stdout() io.Reader { return c.sess.Stdout }

// Stderr is the remote error stream.
func (c *Controller) Stderr() io.Reader { return c.sess.Stderr }

// Input forwards locally-typed bytes to the remote stdin.
func (c *Controller) Input(p []byte) error {
	_, err := c.sess.Stdin.Write(p)
	return err
}

// Resize tells the remote PTY about a new window size. The frontend separately
// resizes its terminal engine.
func (c *Controller) Resize(cols, rows int) error { return c.sess.Resize(cols, rows) }

// Wait blocks until the remote process exits.
func (c *Controller) Wait() error { return c.sess.Wait() }

// Close tears the session down.
func (c *Controller) Close() error { return c.sess.Close() }
