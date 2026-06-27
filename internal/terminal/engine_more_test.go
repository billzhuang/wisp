package terminal

import "testing"

// TestNewTerminalDefaultSize verifies the documented 80x24 default.
func TestNewTerminalDefaultSize(t *testing.T) {
	cols, rows := NewTerminal().Size()
	if cols != 80 || rows != 24 {
		t.Fatalf("default size = %dx%d, want 80x24", cols, rows)
	}
}

// TestSnapshotIndependence verifies Snapshot returns an isolated copy: mutating
// the engine afterwards must not change an already-taken snapshot.
func TestSnapshotIndependence(t *testing.T) {
	e := NewTerminal(WithSize(10, 5))
	writeStr(e, "hello")
	snap := e.Snapshot()
	writeStr(e, "\r\nnew line")
	if snap.Line(1) != "" {
		t.Fatalf("snapshot mutated after later writes: line 1 = %q", snap.Line(1))
	}
	if snap.Line(0) != "hello" {
		t.Fatalf("snapshot line 0 = %q, want hello", snap.Line(0))
	}
}

// TestResizeToSmallerClampsContent ensures shrinking the grid keeps the
// overlapping content and does not panic.
func TestResizeToSmallerClampsContent(t *testing.T) {
	e := NewTerminal(WithSize(10, 5))
	writeStr(e, "abcdefghij")
	e.Resize(4, 3)
	cols, rows := e.Size()
	if cols != 4 || rows != 3 {
		t.Fatalf("size = %dx%d, want 4x3", cols, rows)
	}
	if got := e.Snapshot().Line(0); got != "abcd" {
		t.Fatalf("preserved line = %q, want abcd", got)
	}
}

// TestResizeZeroIgnored verifies a zero/negative dimension resize is a no-op.
func TestResizeZeroIgnored(t *testing.T) {
	e := NewTerminal(WithSize(10, 5))
	e.Resize(0, 5)
	e.Resize(10, 0)
	e.Resize(-1, -1)
	cols, rows := e.Size()
	if cols != 10 || rows != 5 {
		t.Fatalf("zero/negative resize changed size to %dx%d", cols, rows)
	}
}
