//go:build e2e

// Package e2e holds wisp's black-box end-to-end tests: they drive the *actual
// compiled wisp binary* the way a human does — launched as its own process with
// a real PTY for stdio — rather than calling wisp's packages in-process.
//
// The per-layer unit tests and the in-process integration test
// (internal/app/integration_test.go) already prove the seams compose. What they
// can't prove is that the shipped binary, with its real flag parsing, proxy
// startup, raw-mode terminal handling, and stdio frontend, actually launches a
// local shell, forwards keystrokes, and paints the bytes a user would see. That
// is the question only a black-box test of the binary can answer.
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
// The hermetic tests run wisp with `-no-tailnet` so no embedded Tailscale node
// (and thus no network or credentials) is required: the egress proxy dials via
// the OS stack and a real local shell drives the terminal. The opt-in live test
// (tailnet_test.go) exercises the real tsnet path.
package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"bytes"

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

// launch runs wisp in hermetic mode (-no-tailnet, no update check) with the
// given extra args, on a fresh PTY. The returned session is torn down by
// t.Cleanup.
func launch(t *testing.T, args ...string) *session {
	t.Helper()
	base := []string{
		"-no-tailnet",      // proxy via the OS stack; no tsnet/credentials needed
		"-no-update-check", // never touch the network
	}
	return startWisp(t, nil, append(base, args...)...)
}

// startWisp launches the compiled binary with the given args (and any extra
// environment) on a fresh PTY and returns a session that drains and exposes its
// terminal output. It is the low-level spawn shared by the hermetic tests (via
// launch) and the opt-in live tailnet test (which needs real tsnet flags and
// passes its OAuth secret through the environment rather than argv so it never
// lands in a process listing).
func startWisp(t *testing.T, extraEnv []string, args ...string) *session {
	t.Helper()

	cmd := exec.Command(wispBin, args...)
	// TERM makes the child shell's terminal handling well-formed; keep the
	// inherited env so the process resolves runtime deps, plus any caller secrets.
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	cmd.Env = append(cmd.Env, extraEnv...)

	// StartWithSize sets the window size atomically before the child starts, so
	// wisp's stdio frontend reads the intended 80x24 at startup.
	master, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		t.Fatalf("pty.StartWithSize wisp: %v", err)
	}

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

// TestBinaryLaunchesShellAndRenders is the headline check: the real binary
// starts the egress proxy, launches a local shell, and the shell's output
// travels through the engine + frontend to the terminal a user would see. The
// command also prints $WISP and $ALL_PROXY, proving the proxy env was injected.
func TestBinaryLaunchesShellAndRenders(t *testing.T) {
	const banner = "WISP-E2E-LAUNCHED"
	s := launch(t, "-command", `printf '%s wisp=%s proxy=%s\n' "`+banner+`" "$WISP" "$ALL_PROXY"`)

	out := s.waitFor(banner, 10*time.Second)
	if !strings.Contains(out, "wisp=1") {
		t.Fatalf("expected WISP=1 injected into shell env; got:\n%s", sanitize(out))
	}
	if !strings.Contains(out, "proxy=socks5h://") {
		t.Fatalf("expected ALL_PROXY=socks5h://… injected into shell env; got:\n%s", sanitize(out))
	}
}

// TestBinaryForwardsKeystrokes proves the full input path through the real
// binary: bytes typed at wisp's terminal travel client -> shell stdin -> shell,
// and the echo travels back -> engine -> frontend -> terminal. `cat` echoes
// whatever is typed.
func TestBinaryForwardsKeystrokes(t *testing.T) {
	s := launch(t, "-command", "cat")

	s.write("hello-wisp\n")
	out := s.waitFor("hello-wisp", 10*time.Second)
	if !strings.Contains(out, "hello-wisp") {
		t.Fatalf("keystrokes not round-tripped; got:\n%s", sanitize(out))
	}
}

// TestBinaryRunsCommand exercises the `-command` flag end-to-end: wisp runs a
// single command in the shell instead of an interactive session, and its output
// renders.
func TestBinaryRunsCommand(t *testing.T) {
	const marker = "WISP-CMD-RAN"
	s := launch(t, "-command", "echo "+marker)
	out := s.waitFor(marker, 10*time.Second)
	if !strings.Contains(out, marker) {
		t.Fatalf("expected command output %q; got:\n%s", marker, sanitize(out))
	}
}

// TestVersionFlag is the cheapest possible smoke check that the binary runs at
// all: `wisp -version` must print and exit 0 without launching a terminal.
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
