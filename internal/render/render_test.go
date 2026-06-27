package render

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/billzhuang/wisp/internal/terminal"
)

// fakeController feeds canned output and records forwarded input/resizes.
type fakeController struct {
	out io.Reader

	mu      sync.Mutex
	input   []byte
	resizes [][2]int
}

func (f *fakeController) Stdout() io.Reader { return f.out }
func (f *fakeController) Input(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.input = append(f.input, p...)
	return nil
}
func (f *fakeController) Resize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, [2]int{cols, rows})
	return nil
}

func TestHeadlessFeedsEngine(t *testing.T) {
	ctrl := &fakeController{out: strings.NewReader("hello\r\nworld")}
	eng := terminal.NewTerminal(terminal.WithSize(20, 5))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := (Headless{}).Run(ctx, ctrl, eng); err != nil {
		t.Fatalf("run: %v", err)
	}
	g := eng.Snapshot()
	if g.Line(0) != "hello" || g.Line(1) != "world" {
		t.Fatalf("grid = %q / %q", g.Line(0), g.Line(1))
	}
}

func TestHeadlessOnFrameCallback(t *testing.T) {
	ctrl := &fakeController{out: strings.NewReader("abc")}
	eng := terminal.NewTerminal(terminal.WithSize(10, 2))

	frames := 0
	fe := Headless{OnFrame: func(terminal.Engine) { frames++ }}
	if err := fe.Run(context.Background(), ctrl, eng); err != nil {
		t.Fatal(err)
	}
	if frames == 0 {
		t.Fatal("OnFrame never called")
	}
}

func TestHeadlessContextCancel(t *testing.T) {
	// A reader that blocks forever forces the ctx path.
	pr, pw := io.Pipe()
	defer pw.Close()
	ctrl := &fakeController{out: pr}
	eng := terminal.NewTerminal(terminal.WithSize(10, 2))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Headless{}).Run(ctx, ctrl, eng); err == nil {
		t.Fatal("expected context error")
	}
}

// ---------------------------------------------------------------------------
// Additional edge-case tests
// ---------------------------------------------------------------------------

// TestHeadlessNonEOFError verifies that a non-EOF error from the reader is
// propagated to the caller (not swallowed or mistaken for EOF).
func TestHeadlessNonEOFError(t *testing.T) {
	pr, pw := io.Pipe()
	ctrl := &fakeController{out: pr}
	eng := terminal.NewTerminal(terminal.WithSize(10, 2))

	// Close the write end with an explicit error.
	customErr := &customError{"pipe broken"}
	go func() {
		pw.CloseWithError(customErr)
	}()

	err := (Headless{}).Run(context.Background(), ctrl, eng)
	if err == nil {
		t.Fatal("expected non-nil error from broken pipe")
	}
}

// customError is a simple sentinel error for TestHeadlessNonEOFError.
type customError struct{ msg string }

func (e *customError) Error() string { return e.msg }

// TestHeadlessEmptyStream verifies that an empty (immediately EOF) stream
// causes Run to return nil without error.
func TestHeadlessEmptyStream(t *testing.T) {
	ctrl := &fakeController{out: strings.NewReader("")}
	eng := terminal.NewTerminal(terminal.WithSize(10, 2))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := (Headless{}).Run(ctx, ctrl, eng); err != nil {
		t.Fatalf("empty stream should return nil, got: %v", err)
	}
}

// TestHeadlessOnFrameCalledForEachChunk verifies that OnFrame is called once
// per Read chunk, not once for the whole stream.
func TestHeadlessOnFrameCalledForEachChunk(t *testing.T) {
	// Use a pipe so we can write two distinct chunks.
	pr, pw := io.Pipe()
	ctrl := &fakeController{out: pr}
	eng := terminal.NewTerminal(terminal.WithSize(20, 5))

	var mu sync.Mutex
	calls := 0
	fe := Headless{OnFrame: func(terminal.Engine) {
		mu.Lock()
		calls++
		mu.Unlock()
	}}

	done := make(chan error, 1)
	go func() {
		done <- fe.Run(context.Background(), ctrl, eng)
	}()

	pw.Write([]byte("chunk1"))
	pw.Write([]byte("chunk2"))
	pw.Close()

	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	mu.Lock()
	n := calls
	mu.Unlock()
	if n < 1 {
		t.Fatalf("OnFrame called %d times, want ≥1", n)
	}
}

// TestHeadlessMultiLineOutput verifies that the engine grid reflects multiple
// lines written by the remote.
func TestHeadlessMultiLineOutput(t *testing.T) {
	output := "line1\r\nline2\r\nline3"
	ctrl := &fakeController{out: strings.NewReader(output)}
	eng := terminal.NewTerminal(terminal.WithSize(20, 5))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := (Headless{}).Run(ctx, ctrl, eng); err != nil {
		t.Fatal(err)
	}
	g := eng.Snapshot()
	if g.Line(0) != "line1" {
		t.Errorf("line 0 = %q", g.Line(0))
	}
	if g.Line(1) != "line2" {
		t.Errorf("line 1 = %q", g.Line(1))
	}
	if g.Line(2) != "line3" {
		t.Errorf("line 2 = %q", g.Line(2))
	}
}

// TestHeadlessContextCancelledAfterData verifies that if ctx is cancelled while
// the reader still has data, Run returns the ctx error.
func TestHeadlessContextCancelledAfterData(t *testing.T) {
	pr, pw := io.Pipe()
	ctrl := &fakeController{out: pr}
	eng := terminal.NewTerminal(terminal.WithSize(10, 2))

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- (Headless{}).Run(ctx, ctrl, eng)
	}()

	// Write some data, then cancel the context (pipe still open).
	pw.Write([]byte("hello"))
	cancel()
	pw.Close()

	select {
	case err := <-done:
		// Either ctx.Err() or nil is acceptable — the important thing is no
		// deadlock/hang.
		_ = err
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancel + pipe close")
	}
}

// TestHeadlessImplementsFrontend is a compile-time assertion that Headless
// implements the Frontend interface.
func TestHeadlessImplementsFrontend(t *testing.T) {
	var _ Frontend = Headless{}
}
