//go:build e2e

// Package e2e holds wisp's black-box end-to-end tests: they drive the *actual
// compiled wisp binary* the way a human does — launched as its own process with
// a real PTY for stdio — rather than calling wisp's packages in-process.
//
// The per-layer unit tests and the in-process integration test
// (internal/app/integration_test.go) already prove the seams compose. What they
// can't prove is that the shipped binary, with its real flag parsing, auth
// prompt, raw-mode terminal handling, and stdio frontend, actually connects to
// a host, runs a shell, forwards keystrokes, and paints the bytes a user would
// see. That is the question only a black-box test of the binary can answer, and
// it is the one that matters for "does wisp work as a terminal".
//
// These tests are gated behind the `e2e` build tag so the default
// `go test ./...` (which has no PTY and no built binary) stays fast and
// hermetic. Run them with:
//
//	go test -tags e2e ./internal/e2e/...
//
// or via scripts/autotest.sh, which is what the Claude Code auto-test loop
// invokes.
//
// The remote end is the in-process test SSH server (internal/testutil/sshserver)
// listening on localhost; wisp reaches it with `-direct` (no tailnet) and
// `-insecure-host-key` (no known_hosts). No network, display, or Tailscale
// daemon is involved, so the suite runs unchanged in CI.
package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billzhuang/wisp/internal/testutil/sshserver"
	"github.com/creack/pty"
)

// wispBin is the path to the binary compiled once for the whole suite by
// TestMain. Building once keeps each test to a process spawn rather than a
// recompile.
var wispBin string

func TestMain(m *testing.M) {
	bin, cleanup, err := buildWisp()
	if err != nil {
		fmt.Fprintln(os.Stderr, "e2e: build wisp:", err)
		os.Exit(1)
	}
	wispBin = bin
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// buildWisp compiles the default (pure-Go, stdio frontend) wisp binary to a
// temp file and returns its path plus a cleanup func. This is the exact binary
// users get from `go install` / the CLI release flavor.
func buildWisp() (path string, cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "wisp-e2e")
	if err != nil {
		return "", nil, err
	}
	bin := dir + "/wisp"
	// ./cmd/wisp resolved from the module root. `go test` runs with the working
	// directory set to this package's dir (internal/e2e), so reach up to the
	// module root.
	cmd := exec.Command("go", "build", "-o", bin, "github.com/billzhuang/wisp/cmd/wisp")
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("go build: %v: %s", err, out)
	}
	return bin, func() { os.RemoveAll(dir) }, nil
}

// session is a running wisp process attached to a PTY, with the master side
// exposed for reading rendered output and writing keystrokes.
type session struct {
	t      *testing.T
	cmd    *exec.Cmd
	master *os.File

	mu  sync.Mutex
	buf bytes.Buffer // everything wisp has written to its terminal so far
}

// launch starts the in-process SSH server with the given handler, then launches
// wisp against it over a PTY. The returned session is torn down by t.Cleanup.
func launch(t *testing.T, handler sshserver.Handler, args ...string) (*session, *sshserver.Server) {
	t.Helper()

	srv, err := sshserver.Start(handler)
	if err != nil {
		t.Fatalf("start ssh server: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	base := []string{
		"-direct",            // plain TCP, no tsnet/tailnet
		"-insecure-host-key", // no known_hosts file needed
		"-no-update-check",   // never touch the network
		"-host", srv.Addr(),
		"-user", "tester",
	}
	return startWisp(t, append(base, args...)...), srv
}

// startWisp launches the compiled binary with the given args on a fresh PTY and
// returns a session that drains and exposes its terminal output. It is the
// low-level spawn shared by the hermetic localhost tests (via launch) and the
// opt-in live tailnet test (which needs a different flag set: real tsnet, no
// -direct).
func startWisp(t *testing.T, args ...string) *session {
	t.Helper()

	cmd := exec.Command(wispBin, args...)
	// TERM makes the remote pty-req well-formed; keep the inherited env so the
	// process resolves runtime deps and (for the live test) any TS_* vars.
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	master, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start wisp: %v", err)
	}
	// Set an initial window size so the stdio frontend reports sane dimensions.
	_ = pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80})

	s := &session{t: t, cmd: cmd, master: master}

	// Continuously drain the master into buf; the PTY would otherwise block the
	// child once its output buffer fills.
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := master.Read(chunk)
			if n > 0 {
				s.mu.Lock()
				s.buf.Write(chunk[:n])
				s.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		master.Close()
	})

	return s
}

// output returns everything wisp has rendered so far.
func (s *session) output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// write sends bytes to wisp's stdin (as if typed at the terminal).
func (s *session) write(data string) {
	s.t.Helper()
	if _, err := s.master.WriteString(data); err != nil {
		s.t.Fatalf("write to pty: %v", err)
	}
}

// waitFor blocks until wisp's rendered output contains want, or fails the test
// after timeout. It returns the full output captured so far on success.
func (s *session) waitFor(want string, timeout time.Duration) string {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if out := s.output(); strings.Contains(out, want) {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
	out := s.output()
	s.t.Fatalf("timed out after %s waiting for %q.\n--- wisp output so far ---\n%s\n--- end ---",
		timeout, want, sanitize(out))
	return out
}

// sanitize makes captured terminal bytes readable in failure messages by
// stripping the most common escape noise while keeping printable content.
func sanitize(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\x1b':
			b.WriteString("<ESC>")
		case c == '\r':
			// drop; \n carries the line break
		case c >= 0x20 || c == '\n' || c == '\t':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "<%02x>", c)
		}
	}
	return b.String()
}

// TestBinaryConnectsAndRenders is the headline check: the real binary dials the
// host, the SSH+PTY+engine+frontend pipeline carries remote output, and the
// bytes a user would see actually reach the terminal.
func TestBinaryConnectsAndRenders(t *testing.T) {
	const banner = "WISP-E2E-CONNECTED"
	s, _ := launch(t, func(stdin io.Reader, stdout io.Writer, cmd string, pty sshserver.PTYRequest) {
		fmt.Fprint(stdout, "\x1b[2J\x1b[H") // clear + home, like a real shell prompt
		fmt.Fprintf(stdout, "%s term=%s %dx%d\r\n", banner, pty.Term, pty.Cols, pty.Rows)
		// Keep the session open briefly so the client is unambiguously connected.
		buf := make([]byte, 1)
		stdin.Read(buf)
	})

	// Empty password (the test server accepts any); the interactive prompt reads
	// one line from the PTY.
	s.write("\n")

	out := s.waitFor(banner, 10*time.Second)
	if !strings.Contains(out, "term=xterm-256color") {
		t.Fatalf("expected TERM forwarded to remote pty-req; got:\n%s", sanitize(out))
	}
	// The remote saw an 80x24 window (the size we set on the PTY).
	if !strings.Contains(out, "80x24") {
		t.Fatalf("expected forwarded window size 80x24; got:\n%s", sanitize(out))
	}
}

// TestBinaryForwardsKeystrokes proves the full input path through the real
// binary: bytes typed at wisp's terminal travel client -> SSH stdin -> remote,
// and the remote's echo travels back -> engine -> frontend -> terminal.
func TestBinaryForwardsKeystrokes(t *testing.T) {
	s, _ := launch(t, func(stdin io.Reader, stdout io.Writer, cmd string, pty sshserver.PTYRequest) {
		fmt.Fprint(stdout, "ready\r\n")
		r := make([]byte, 256)
		for {
			n, err := stdin.Read(r)
			if n > 0 {
				// Echo back what was typed, framed so the test can find it.
				fmt.Fprintf(stdout, "ECHO[%s]\r\n", strings.TrimRight(string(r[:n]), "\r\n"))
			}
			if err != nil {
				return
			}
		}
	})

	s.write("\n") // password
	s.waitFor("ready", 10*time.Second)

	s.write("hello-wisp\n")
	out := s.waitFor("ECHO[hello-wisp]", 10*time.Second)
	if !strings.Contains(out, "ECHO[hello-wisp]") {
		t.Fatalf("keystrokes not round-tripped; got:\n%s", sanitize(out))
	}
}

// TestBinaryRunsRemoteCommand exercises the `-command` flag end-to-end: wisp
// requests an exec channel instead of a shell, and the command's output renders.
func TestBinaryRunsRemoteCommand(t *testing.T) {
	const marker = "REMOTE-CMD-RAN"
	s, srv := launch(t, func(stdin io.Reader, stdout io.Writer, cmd string, pty sshserver.PTYRequest) {
		fmt.Fprintf(stdout, "%s: %s\r\n", marker, cmd)
	}, "-command", "echo hi")
	_ = srv

	s.write("\n") // password
	out := s.waitFor(marker, 10*time.Second)
	if !strings.Contains(out, "echo hi") {
		t.Fatalf("expected remote to receive command %q; got:\n%s", "echo hi", sanitize(out))
	}
}

// TestVersionFlag is the cheapest possible smoke check that the binary runs at
// all: `wisp -version` must print and exit 0 without connecting.
func TestVersionFlag(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, wispBin, "-version").CombinedOutput()
	if err != nil {
		t.Fatalf("wisp -version: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "wisp") || !strings.Contains(string(out), "engine:") {
		t.Fatalf("unexpected -version output: %q", out)
	}
}
