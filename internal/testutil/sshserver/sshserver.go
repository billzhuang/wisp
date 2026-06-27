// Package sshserver is a minimal in-process SSH server used by wisp's tests. It
// performs a real SSH handshake (x/crypto/ssh server side), accepts a session
// channel, answers pty-req / window-change / shell / exec, and runs a
// caller-supplied handler whose output is written back over the channel. This
// lets the network → SSH → PTY → terminal-engine seam be tested end-to-end
// without a real tailnet or a system sshd.
package sshserver

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"
)

// PTYRequest captures the parameters of a pty-req for assertions.
type PTYRequest struct {
	Term       string
	Cols, Rows uint32
}

// Handler runs a fake remote program. stdin carries bytes typed by the client;
// write program output to stdout. cmd is the exec command ("" for an
// interactive shell). pty describes the allocated PTY. Returning ends the
// session with exit status 0.
type Handler func(stdin io.Reader, stdout io.Writer, cmd string, pty PTYRequest)

// Server is a running test SSH server.
type Server struct {
	ln       net.Listener
	cfg      *ssh.ServerConfig
	handler  Handler
	signer   ssh.Signer
	wg       sync.WaitGroup
	mu       sync.Mutex
	resizes  []PTYRequest // window-change events observed, for assertions
	password string
	conns    []net.Conn // accepted connections, closed proactively on Close
	closing  bool
}

// Option configures the server.
type Option func(*Server)

// WithPassword requires the given password for authentication. By default the
// server accepts any password.
func WithPassword(pw string) Option { return func(s *Server) { s.password = pw } }

// Start launches a server on a random localhost port. Call Close to stop it.
func Start(handler Handler, opts ...Option) (*Server, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, err
	}

	s := &Server{handler: handler, signer: signer}
	for _, o := range opts {
		o(s)
	}

	s.cfg = &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if s.password != "" && string(pass) != s.password {
				return nil, fmt.Errorf("auth failed")
			}
			return &ssh.Permissions{}, nil
		},
	}
	s.cfg.AddHostKey(signer)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.ln = ln

	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// Addr is the "host:port" the server listens on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// HostKey is the server's public host key (for known_hosts in tests).
func (s *Server) HostKey() ssh.PublicKey { return s.signer.PublicKey() }

// Resizes returns the window-change events observed so far.
func (s *Server) Resizes() []PTYRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PTYRequest, len(s.resizes))
	copy(out, s.resizes)
	return out
}

// Close stops the server and waits for in-flight connections to drain. It
// proactively closes accepted connections so it never depends on the client
// disconnecting first — a test that forgets to close its client still shuts
// down cleanly rather than hanging.
func (s *Server) Close() error {
	err := s.ln.Close()
	s.mu.Lock()
	s.closing = true
	conns := s.conns
	s.conns = nil
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	s.wg.Wait()
	return err
}

func (s *Server) serve() {
	defer s.wg.Done()
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return // listener closed
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(nc)
		}()
	}
}

func (s *Server) handleConn(nc net.Conn) {
	defer nc.Close()
	// Track the connection so Close can tear it down proactively.
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.conns = append(s.conns, nc)
	s.mu.Unlock()

	conn, chans, reqs, err := ssh.NewServerConn(nc, s.cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		if newCh.ChannelType() != "session" {
			newCh.Reject(ssh.UnknownChannelType, "only session channels supported")
			continue
		}
		ch, chReqs, err := newCh.Accept()
		if err != nil {
			return
		}
		go s.handleSession(ch, chReqs)
	}
}

func (s *Server) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	var pty PTYRequest
	started := false
	// The request loop must keep running after shell/exec so that
	// window-change events arriving mid-session are still processed; the
	// handler therefore runs in its own goroutine.
	for req := range reqs {
		switch req.Type {
		case "pty-req":
			term, cols, rows, ok := parsePtyReq(req.Payload)
			if ok {
				pty = PTYRequest{Term: term, Cols: cols, Rows: rows}
			}
			req.Reply(true, nil)
		case "window-change":
			cols, rows, ok := parseWindowChange(req.Payload)
			if ok {
				s.mu.Lock()
				s.resizes = append(s.resizes, PTYRequest{Cols: cols, Rows: rows})
				s.mu.Unlock()
			}
			// window-change wants no reply.
		case "shell":
			req.Reply(true, nil)
			if !started {
				started = true
				go s.runHandler(ch, "", pty)
			}
		case "exec":
			cmd := parseString(req.Payload)
			req.Reply(true, nil)
			if !started {
				started = true
				go s.runHandler(ch, cmd, pty)
			}
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

func (s *Server) runHandler(ch ssh.Channel, cmd string, pty PTYRequest) {
	s.handler(ch, ch, cmd, pty)
	// Signal clean exit and close the channel.
	ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
	ch.Close()
}

// --- SSH payload parsing (RFC 4254 wire format) ---

func parseString(b []byte) string {
	if len(b) < 4 {
		return ""
	}
	n := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	if int(n)+4 > len(b) {
		return ""
	}
	return string(b[4 : 4+n])
}

func parsePtyReq(b []byte) (term string, cols, rows uint32, ok bool) {
	// string TERM, uint32 cols, uint32 rows, uint32 widthpx, uint32 heightpx, ...
	if len(b) < 4 {
		return "", 0, 0, false
	}
	n := beUint32(b)
	if int(n)+4+8 > len(b) {
		return "", 0, 0, false
	}
	term = string(b[4 : 4+n])
	rest := b[4+n:]
	cols = beUint32(rest)
	rows = beUint32(rest[4:])
	return term, cols, rows, true
}

func parseWindowChange(b []byte) (cols, rows uint32, ok bool) {
	// uint32 cols, uint32 rows, uint32 widthpx, uint32 heightpx
	if len(b) < 8 {
		return 0, 0, false
	}
	return beUint32(b), beUint32(b[4:]), true
}

func beUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
