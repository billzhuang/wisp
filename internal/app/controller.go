// Package app wires the local-shell half of wisp into something a frontend can
// drive. A Controller owns a live shell session running in a local PTY: it
// exposes the shell's output stream, forwards user input to the shell's stdin,
// and propagates window resizes to the PTY (the frontend separately resizes the
// terminal engine).
//
// The controller is deliberately ignorant of rendering and of the tailnet. A
// render.Frontend consumes a Controller through the small render.Controller
// interface, so the same controller drives the headless test frontend, the
// stdio passthrough, or the Ebitengine GUI without change. Tailnet access is
// arranged out-of-band: main starts a proxy and injects its address into the
// shell's environment, so the controller just runs whatever command it is given.
package app

import (
	"io"
	"sync"

	"github.com/billzhuang/wisp/internal/localpty"
)

// Controller manages one local shell session for a frontend.
type Controller struct {
	sess *localpty.Session

	closeOnce sync.Once
	closeErr  error
}

// NewLocal returns a controller for a not-yet-started local shell described by
// cfg. The caller calls Start to spawn it and Close to tear it down.
func NewLocal(cfg localpty.Config) *Controller {
	return &Controller{sess: localpty.New(cfg)}
}

// NewController wraps an already-prepared session (used by tests).
func NewController(sess *localpty.Session) *Controller { return &Controller{sess: sess} }

// Start spawns the local shell on its PTY.
func (c *Controller) Start() error { return c.sess.Start() }

// Stdout is the shell's combined output stream. A frontend copies this into its
// terminal engine.
func (c *Controller) Stdout() io.Reader { return c.sess.Stdout() }

// Input forwards locally-typed bytes to the shell's stdin.
func (c *Controller) Input(p []byte) error { return c.sess.Input(p) }

// Resize tells the PTY about a new window size. The frontend separately resizes
// its terminal engine.
func (c *Controller) Resize(cols, rows int) error { return c.sess.Resize(cols, rows) }

// Wait blocks until the shell exits.
func (c *Controller) Wait() error { return c.sess.Wait() }

// Close tears the session down. It is idempotent: a tabbed frontend's session
// manager and main's defer may both close the same controller, so the second
// call is a no-op that returns the first call's result.
func (c *Controller) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.sess.Close() })
	return c.closeErr
}
