package app_test

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/billzhuang/wisp/internal/app"
	"github.com/billzhuang/wisp/internal/render"
	"github.com/billzhuang/wisp/internal/terminal"
)

// fakeCtrl is a render.Controller backed by an in-memory pipe: tests push output
// through pw, and Input/Resize/close are recorded for assertions.
type fakeCtrl struct {
	pr *io.PipeReader
	pw *io.PipeWriter

	mu      sync.Mutex
	input   []byte
	resizes [][2]int
	closes  int
}

func newFakeCtrl() *fakeCtrl {
	pr, pw := io.Pipe()
	return &fakeCtrl{pr: pr, pw: pw}
}

func (f *fakeCtrl) Stdout() io.Reader { return f.pr }

func (f *fakeCtrl) Input(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.input = append(f.input, p...)
	return nil
}

func (f *fakeCtrl) Resize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, [2]int{cols, rows})
	return nil
}

// close unblocks the pump (its Read returns EOF) and records the call.
func (f *fakeCtrl) close() error {
	f.mu.Lock()
	f.closes++
	f.mu.Unlock()
	return f.pw.Close()
}

func (f *fakeCtrl) inputStr() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.input)
}

func (f *fakeCtrl) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

func (f *fakeCtrl) lastResize() ([2]int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.resizes) == 0 {
		return [2]int{}, false
	}
	return f.resizes[len(f.resizes)-1], true
}

// opened records each session the opener manufactures.
type opened struct {
	ctrl *fakeCtrl
	eng  terminal.Engine
}

func openerFor(made *[]opened) app.Opener {
	return func() (render.Controller, terminal.Engine, func() error, error) {
		c := newFakeCtrl()
		e := terminal.DefaultEngine(80, 24)
		*made = append(*made, opened{c, e})
		return c, e, c.close, nil
	}
}

func failOpener(t *testing.T) app.Opener {
	return func() (render.Controller, terminal.Engine, func() error, error) {
		t.Helper()
		t.Error("opener should not have been called")
		return nil, nil, nil, errors.New("unexpected open")
	}
}

func activeIndex(tabs *app.Tabs) int {
	for i, ti := range tabs.TabList() {
		if ti.Active {
			return i
		}
	}
	return -1
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestTabsStartsWithOneActiveTab(t *testing.T) {
	first := newFakeCtrl()
	feng := terminal.DefaultEngine(80, 24)
	tabs := app.NewTabs(first, feng, first.close, failOpener(t))
	defer tabs.Close()

	list := tabs.TabList()
	if len(list) != 1 {
		t.Fatalf("len(TabList) = %d, want 1", len(list))
	}
	if list[0].Title != "#1" || !list[0].Active {
		t.Fatalf("tab[0] = %+v, want {Title:#1 Active:true}", list[0])
	}
	if tabs.ActiveEngine() != feng {
		t.Fatal("ActiveEngine should be the first tab's engine")
	}
}

func TestNewTabAddsAndActivates(t *testing.T) {
	first := newFakeCtrl()
	var made []opened
	tabs := app.NewTabs(first, terminal.DefaultEngine(80, 24), first.close, openerFor(&made))
	defer tabs.Close()

	if err := tabs.NewTab(); err != nil {
		t.Fatalf("NewTab: %v", err)
	}

	list := tabs.TabList()
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	if list[0].Active || !list[1].Active {
		t.Fatalf("new tab should be the active one: %+v", list)
	}
	if list[1].Title != "#2" {
		t.Fatalf("title = %q, want #2", list[1].Title)
	}
	if len(made) != 1 {
		t.Fatalf("opener called %d times, want 1", len(made))
	}
	if tabs.ActiveEngine() != made[0].eng {
		t.Fatal("ActiveEngine should be the new tab's engine")
	}
}

func TestNextPrevTabWrap(t *testing.T) {
	first := newFakeCtrl()
	var made []opened
	tabs := app.NewTabs(first, terminal.DefaultEngine(80, 24), first.close, openerFor(&made))
	defer tabs.Close()
	mustNewTab(t, tabs) // #2
	mustNewTab(t, tabs) // #3, now active

	if got := activeIndex(tabs); got != 2 {
		t.Fatalf("active = %d, want 2", got)
	}
	for _, step := range []struct {
		move func()
		want int
	}{
		{tabs.NextTab, 0}, // wrap forward
		{tabs.NextTab, 1},
		{tabs.PrevTab, 0},
		{tabs.PrevTab, 2}, // wrap backward
	} {
		step.move()
		if got := activeIndex(tabs); got != step.want {
			t.Fatalf("active = %d, want %d", got, step.want)
		}
	}
}

func TestInputRoutesToActiveTabOnly(t *testing.T) {
	first := newFakeCtrl()
	var made []opened
	tabs := app.NewTabs(first, terminal.DefaultEngine(80, 24), first.close, openerFor(&made))
	defer tabs.Close()
	mustNewTab(t, tabs) // active = made[0]

	if err := tabs.Input([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := made[0].ctrl.inputStr(); got != "hello" {
		t.Fatalf("active tab input = %q, want hello", got)
	}
	if got := first.inputStr(); got != "" {
		t.Fatalf("background tab received %q, want nothing", got)
	}

	tabs.PrevTab() // back to the first tab
	if err := tabs.Input([]byte("world")); err != nil {
		t.Fatal(err)
	}
	if got := first.inputStr(); got != "world" {
		t.Fatalf("first tab input = %q, want world", got)
	}
}

func TestResizeFansOutToEveryTab(t *testing.T) {
	first := newFakeCtrl()
	feng := terminal.DefaultEngine(80, 24)
	var made []opened
	tabs := app.NewTabs(first, feng, first.close, openerFor(&made))
	defer tabs.Close()
	mustNewTab(t, tabs)
	beng := made[0].eng

	if err := tabs.Resize(100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	if c, r := feng.Size(); c != 100 || r != 30 {
		t.Fatalf("first engine = %dx%d, want 100x30", c, r)
	}
	if c, r := beng.Size(); c != 100 || r != 30 {
		t.Fatalf("background engine = %dx%d, want 100x30", c, r)
	}
	for name, fc := range map[string]*fakeCtrl{"first": first, "second": made[0].ctrl} {
		if last, ok := fc.lastResize(); !ok || last != [2]int{100, 30} {
			t.Fatalf("%s remote PTY last resize = %v (ok=%v), want [100 30]", name, last, ok)
		}
	}
}

func TestPumpFeedsActiveEngineAndClearsLoading(t *testing.T) {
	first := newFakeCtrl()
	feng := terminal.DefaultEngine(80, 24)
	tabs := app.NewTabs(first, feng, first.close, failOpener(t))
	defer tabs.Close()

	if !tabs.ActiveLoading() {
		t.Fatal("a fresh tab should report loading until its first output")
	}
	if _, err := first.pw.Write([]byte("hi there")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitFor(t, func() bool { return feng.Snapshot().Line(0) == "hi there" })
	if tabs.ActiveLoading() {
		t.Fatal("loading should clear once output has arrived")
	}
}

func TestCloseActiveTabKeepsAtLeastOne(t *testing.T) {
	first := newFakeCtrl()
	var made []opened
	tabs := app.NewTabs(first, terminal.DefaultEngine(80, 24), first.close, openerFor(&made))
	defer tabs.Close()
	mustNewTab(t, tabs) // active = second (index 1)
	second := made[0].ctrl

	tabs.CloseActiveTab()
	if got := len(tabs.TabList()); got != 1 {
		t.Fatalf("after close, len = %d, want 1", got)
	}
	waitFor(t, func() bool { return second.closeCount() == 1 })
	if !tabs.TabList()[0].Active {
		t.Fatal("the surviving tab should become active")
	}

	// Closing the last remaining tab is a no-op: there is always one tab.
	tabs.CloseActiveTab()
	if got := len(tabs.TabList()); got != 1 {
		t.Fatalf("closing the last tab changed count to %d", got)
	}
	if first.closeCount() != 0 {
		t.Fatalf("the last tab must not be closed; closeCount = %d", first.closeCount())
	}
}

func TestCloseClosesEveryTabIdempotently(t *testing.T) {
	first := newFakeCtrl()
	var made []opened
	tabs := app.NewTabs(first, terminal.DefaultEngine(80, 24), first.close, openerFor(&made))
	mustNewTab(t, tabs)
	mustNewTab(t, tabs)

	if err := tabs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitFor(t, func() bool {
		return first.closeCount() == 1 && made[0].ctrl.closeCount() == 1 && made[1].ctrl.closeCount() == 1
	})

	if err := tabs.Close(); err != nil { // idempotent
		t.Fatalf("second Close: %v", err)
	}
	if first.closeCount() != 1 {
		t.Fatalf("first tab closed %d times, want exactly 1", first.closeCount())
	}
}

func TestNewTabErrorDoesNotAddTab(t *testing.T) {
	first := newFakeCtrl()
	wantErr := errors.New("dial failed")
	open := func() (render.Controller, terminal.Engine, func() error, error) {
		return nil, nil, nil, wantErr
	}
	tabs := app.NewTabs(first, terminal.DefaultEngine(80, 24), first.close, open)
	defer tabs.Close()

	if err := tabs.NewTab(); !errors.Is(err, wantErr) {
		t.Fatalf("NewTab err = %v, want %v", err, wantErr)
	}
	if got := len(tabs.TabList()); got != 1 {
		t.Fatalf("a failed NewTab should not add a tab; len = %d", got)
	}
}

func TestStdoutIsInertInTabMode(t *testing.T) {
	first := newFakeCtrl()
	tabs := app.NewTabs(first, terminal.DefaultEngine(80, 24), first.close, failOpener(t))
	defer tabs.Close()

	n, err := tabs.Stdout().Read(make([]byte, 8))
	if n != 0 || err != io.EOF {
		t.Fatalf("Stdout().Read = (%d, %v), want (0, EOF)", n, err)
	}
}

func mustNewTab(t *testing.T, tabs *app.Tabs) {
	t.Helper()
	if err := tabs.NewTab(); err != nil {
		t.Fatalf("NewTab: %v", err)
	}
}
