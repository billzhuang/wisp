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
