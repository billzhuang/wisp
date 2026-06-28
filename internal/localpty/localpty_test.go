package localpty

import (
	"bytes"
	"io"
	"testing"
	"time"
)

// TestRunCommandOutput spawns a shell running a one-shot command and verifies
// its output is readable from the PTY master.
func TestRunCommandOutput(t *testing.T) {
	s := New(Config{
		Path: "/bin/sh",
		Args: []string{"/bin/sh", "-c", "printf hi"},
		Cols: 40, Rows: 10,
	})
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()

	// Read until the shell exits (PTY closes), then check the captured bytes.
	out := readWithTimeout(t, s.Stdout(), 3*time.Second)
	if !bytes.Contains(out, []byte("hi")) {
		t.Fatalf("output = %q, want it to contain %q", out, "hi")
	}
}

// TestInputRoundTrip feeds bytes to a `cat` shell and reads them back.
func TestInputRoundTrip(t *testing.T) {
	s := New(Config{
		Path: "/bin/sh",
		Args: []string{"/bin/sh", "-c", "cat"},
		Cols: 40, Rows: 10,
	})
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()

	if err := s.Input([]byte("ping\n")); err != nil {
		t.Fatalf("input: %v", err)
	}

	buf := make([]byte, 64)
	deadline := time.Now().Add(3 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		s.ptmx.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, _ := s.Stdout().Read(buf)
		got = append(got, buf[:n]...)
		if bytes.Contains(got, []byte("ping")) {
			return
		}
	}
	t.Fatalf("never read echoed input; got %q", got)
}

// TestResizeBeforeStartIsNoOp verifies Resize is safe before the process exists,
// and works after Start.
func TestResizeBeforeStartIsNoOp(t *testing.T) {
	s := New(Config{Path: "/bin/sh", Args: []string{"/bin/sh", "-c", "sleep 1"}})
	if err := s.Resize(100, 30); err != nil {
		t.Fatalf("resize before start should be a no-op, got %v", err)
	}
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	if err := s.Resize(120, 40); err != nil {
		t.Fatalf("resize after start: %v", err)
	}
}

// TestStartTwiceErrors guards the single-start contract.
func TestStartTwiceErrors(t *testing.T) {
	s := New(Config{Path: "/bin/sh", Args: []string{"/bin/sh", "-c", "true"}})
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer s.Close()
	if err := s.Start(); err == nil {
		t.Fatal("expected error on second Start")
	}
}

// TestMissingPathErrors verifies an empty command path is rejected.
func TestMissingPathErrors(t *testing.T) {
	s := New(Config{})
	if err := s.Start(); err == nil {
		t.Fatal("expected error for empty command path")
	}
}

// readWithTimeout drains r until EOF or the timeout, returning what it read.
func readWithTimeout(t *testing.T, r io.Reader, d time.Duration) []byte {
	t.Helper()
	done := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- b
	}()
	select {
	case b := <-done:
		return b
	case <-time.After(d):
		t.Fatalf("timed out reading PTY output after %s", d)
		return nil
	}
}
