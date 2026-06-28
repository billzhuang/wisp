// Package localpty runs a local command — a login shell by default — inside a
// pseudo-terminal and exposes its stdio plus window-resize, so the terminal
// engine and renderer can drive it exactly as they drove the old remote SSH
// session. It is the local counterpart to the former internal/sshx: the same
// Session surface (Start/Stdout/Input/Resize/Wait/Close), but the process runs
// on this machine instead of across the tailnet.
//
// The point of running the shell here (rather than SSHing somewhere) is that
// wisp can inject a tailnet egress proxy into the child's environment: tools the
// user runs in the shell reach tailnet resources through that proxy, with no
// Tailscale app installed. localpty itself knows nothing about the proxy — it
// just launches whatever command and environment it is handed.
package localpty

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

// Config describes the command to run in the PTY.
type Config struct {
	// Path is the program to execute (e.g. /bin/zsh). Required.
	Path string
	// Args is the full argv, including argv[0]. If nil, []string{Path} is used.
	// A login shell is requested by setting argv[0] to "-<base>" (e.g. "-zsh").
	Args []string
	// Env is the environment for the child. If nil, the parent's environment is
	// used. main injects the tailnet proxy variables here.
	Env []string
	// Dir is the working directory; empty inherits the parent's.
	Dir string
	// Term is the TERM value exported to the child if Env does not already set
	// one.
	Term string
	// Cols, Rows are the initial PTY dimensions.
	Cols, Rows int
}

func (c *Config) withDefaults() {
	if c.Term == "" {
		c.Term = "xterm-256color"
	}
	if c.Cols == 0 {
		c.Cols = 80
	}
	if c.Rows == 0 {
		c.Rows = 24
	}
}

// Session is a command running on an allocated PTY.
type Session struct {
	cfg  Config
	cmd  *exec.Cmd
	ptmx *os.File

	waitOnce sync.Once
	waitErr  error
}

// New prepares a session for cfg. The process is not spawned until Start.
func New(cfg Config) *Session {
	cfg.withDefaults()
	return &Session{cfg: cfg}
}

// Start spawns the command on a fresh PTY sized to the config. It is an error to
// call Start twice.
func (s *Session) Start() error {
	if s.cmd != nil {
		return fmt.Errorf("localpty: already started")
	}
	if s.cfg.Path == "" {
		return fmt.Errorf("localpty: command path is required")
	}

	argv := s.cfg.Args
	if argv == nil {
		argv = []string{s.cfg.Path}
	}
	env := s.cfg.Env
	if env == nil {
		env = os.Environ()
	}
	env = ensureTerm(env, s.cfg.Term)

	cmd := &exec.Cmd{
		Path: s.cfg.Path,
		Args: argv,
		Env:  env,
		Dir:  s.cfg.Dir,
	}

	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: uint16(s.cfg.Rows),
		Cols: uint16(s.cfg.Cols),
	})
	if err != nil {
		return fmt.Errorf("localpty: start %s: %w", s.cfg.Path, err)
	}
	s.cmd = cmd
	s.ptmx = ptmx
	return nil
}

// Stdout is the PTY master: the shell's combined stdout+stderr. A frontend
// copies this into its terminal engine. It is valid only after Start.
//
// The returned reader translates the EIO that Linux returns from a PTY master
// once the child exits into a clean io.EOF, so callers see normal end-of-stream
// rather than a spurious error.
func (s *Session) Stdout() io.Reader { return ptyReader{s.ptmx} }

// ptyReader adapts a PTY master so that the EIO a Linux master returns when the
// child has exited reads as io.EOF.
type ptyReader struct{ f *os.File }

func (r ptyReader) Read(p []byte) (int, error) {
	n, err := r.f.Read(p)
	if err != nil && errors.Is(err, syscall.EIO) {
		err = io.EOF
	}
	return n, err
}

// Input writes locally-typed bytes to the shell's stdin (the PTY master).
func (s *Session) Input(p []byte) error {
	_, err := s.ptmx.Write(p)
	return err
}

// Resize informs the PTY of a new window size. Safe to call before Start (it is
// then a no-op, since Start sizes the PTY itself).
func (s *Session) Resize(cols, rows int) error {
	if s.ptmx == nil {
		return nil
	}
	return pty.Setsize(s.ptmx, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// Wait blocks until the shell exits.
func (s *Session) Wait() error { return s.wait() }

// Close tears the session down: it closes the PTY, signals the shell to exit,
// and reaps it. It is safe to call alongside Wait — the underlying reap happens
// at most once.
func (s *Session) Close() error {
	if s.ptmx != nil {
		s.ptmx.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.wait()
	}
	return nil
}

func (s *Session) wait() error {
	if s.cmd == nil {
		return nil
	}
	s.waitOnce.Do(func() { s.waitErr = s.cmd.Wait() })
	return s.waitErr
}

// ensureTerm appends TERM=term unless the environment already defines TERM.
func ensureTerm(env []string, term string) []string {
	for _, kv := range env {
		if len(kv) >= 5 && kv[:5] == "TERM=" {
			return env
		}
	}
	return append(append([]string(nil), env...), "TERM="+term)
}
