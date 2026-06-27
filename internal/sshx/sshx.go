// Package sshx layers an interactive SSH session on top of a transport.Dialer.
// It is intentionally transport-agnostic: it dials through the Dialer (tsnet in
// production, plain TCP in tests), performs the SSH handshake with proper
// host-key verification, requests a PTY, and exposes the session's stdin/stdout
// plus window-resize so the terminal engine and renderer can be wired in.
package sshx

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/billzhuang/wisp/internal/transport"
	"golang.org/x/crypto/ssh"
)

// Config describes how to connect and authenticate.
type Config struct {
	// Addr is the destination "host:port". host may be a MagicDNS name when
	// dialing through tsnet.
	Addr string

	// User is the remote login name.
	User string

	// Auth holds the authentication methods to try, in order.
	Auth []ssh.AuthMethod

	// HostKey verifies the server's host key. Required — wisp never silently
	// accepts unknown keys. Use KnownHostsCallback or, for tests only,
	// InsecureIgnoreHostKey.
	HostKey ssh.HostKeyCallback

	// Term is the TERM value advertised to the remote PTY.
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

// Session is a live SSH session with an allocated PTY.
type Session struct {
	client *ssh.Client
	sess   *ssh.Session
	conn   net.Conn

	Stdin  io.WriteCloser
	Stdout io.Reader
	Stderr io.Reader
}

// Dial connects through the dialer, completes the SSH handshake and opens a
// session with a PTY allocated. The caller then starts a shell or command via
// Shell/Run and pipes Stdout into a terminal engine.
func Dial(ctx context.Context, d transport.Dialer, cfg Config) (*Session, error) {
	cfg.withDefaults()
	if cfg.HostKey == nil {
		return nil, fmt.Errorf("sshx: HostKey callback is required")
	}

	conn, err := d.Dial(ctx, "tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("sshx: dial %s: %w", cfg.Addr, err)
	}

	clientCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            cfg.Auth,
		HostKeyCallback: cfg.HostKey,
	}

	// Run the SSH handshake over the transport conn. Honour ctx cancellation
	// by closing the conn, which unblocks the handshake.
	type result struct {
		c *ssh.Client
		e error
	}
	done := make(chan result, 1)
	go func() {
		cc, chans, reqs, err := ssh.NewClientConn(conn, cfg.Addr, clientCfg)
		if err != nil {
			done <- result{nil, err}
			return
		}
		done <- result{ssh.NewClient(cc, chans, reqs), nil}
	}()

	var client *ssh.Client
	select {
	case <-ctx.Done():
		conn.Close()
		// The handshake goroutine may still be in flight; if it produced a live
		// client just as ctx was cancelled, drain and close it so it can't leak.
		go func() {
			if r := <-done; r.c != nil {
				r.c.Close()
			}
		}()
		return nil, ctx.Err()
	case r := <-done:
		if r.e != nil {
			conn.Close()
			return nil, fmt.Errorf("sshx: handshake: %w", r.e)
		}
		client = r.c
	}

	sess, err := client.NewSession()
	if err != nil {
		client.Close()
		conn.Close()
		return nil, fmt.Errorf("sshx: new session: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := sess.RequestPty(cfg.Term, cfg.Rows, cfg.Cols, modes); err != nil {
		sess.Close()
		client.Close()
		conn.Close()
		return nil, fmt.Errorf("sshx: request pty: %w", err)
	}

	stdin, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		client.Close()
		conn.Close()
		return nil, fmt.Errorf("sshx: stdin pipe: %w", err)
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		client.Close()
		conn.Close()
		return nil, fmt.Errorf("sshx: stdout pipe: %w", err)
	}
	stderr, err := sess.StderrPipe()
	if err != nil {
		sess.Close()
		client.Close()
		conn.Close()
		return nil, fmt.Errorf("sshx: stderr pipe: %w", err)
	}

	return &Session{
		client: client,
		sess:   sess,
		conn:   conn,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
	}, nil
}

// Shell starts an interactive login shell on the remote PTY.
func (s *Session) Shell() error { return s.sess.Shell() }

// Run starts a single command on the remote PTY (used by tests and one-shot
// invocations). Unlike (*ssh.Session).Run it does not wait — use Wait.
func (s *Session) Run(cmd string) error { return s.sess.Start(cmd) }

// Resize informs the remote of a new window size. Pair this with the terminal
// engine's Resize on every window-size change.
func (s *Session) Resize(cols, rows int) error {
	return s.sess.WindowChange(rows, cols)
}

// Wait blocks until the remote process exits.
func (s *Session) Wait() error { return s.sess.Wait() }

// Close tears down the session, client and transport conn.
func (s *Session) Close() error {
	var first error
	if s.sess != nil {
		if err := s.sess.Close(); err != nil && err != io.EOF {
			first = err
		}
	}
	if s.client != nil {
		if err := s.client.Close(); err != nil && first == nil {
			first = err
		}
	}
	if s.conn != nil {
		s.conn.Close()
	}
	return first
}
