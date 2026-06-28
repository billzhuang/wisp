package app

import (
	"errors"
	"io"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/billzhuang/wisp/internal/render"
	"github.com/billzhuang/wisp/internal/terminal"
)

// Tabs presents several concurrent SSH sessions to a frontend as tabs. It is the
// production render.TabController: every tab owns its own terminal engine and a
// goroutine pumping that session's output into it (so background tabs stay live),
// Input goes to the active tab, Resize fans out to all tabs (so a background tab
// matches the window the moment it is selected), and New/Close/Next/Prev manage
// the set. There is always at least one tab.
//
// Tabs is safe for concurrent use: the frontend's UI loop, the per-tab pumps,
// and any goroutine opening a tab all touch it through mu.
type Tabs struct {
	open Opener

	mu     sync.Mutex
	tabs   []*tab
	active int
	cols   int // current window size, applied to every tab
	rows   int
	closed bool
}

// Opener dials a brand-new session and pairs it with a fresh engine, returning a
// closer that tears the session down. Tabs calls it for every tab after the
// first. Keeping it a function (rather than wiring in the dialer/config) keeps
// Tabs free of any SSH/transport dependency and trivially testable with fakes.
type Opener func() (ctrl render.Controller, eng terminal.Engine, closer func() error, err error)

// tab is a single session: a controller to drive, an engine to render, the pump
// that connects them, and a closer.
type tab struct {
	ctrl   render.Controller
	eng    terminal.Engine
	closer func() error
	loaded atomic.Bool // set once the first byte of remote output arrives
}

var errClosed = errors.New("app: tab manager closed")

// NewTabs builds a Tabs around an already-established first session — the one
// main dialed at startup — and an opener for every subsequent tab. firstCloser
// tears the first session down (typically firstCtrl's Close).
func NewTabs(firstCtrl render.Controller, firstEng terminal.Engine, firstCloser func() error, open Opener) *Tabs {
	cols, rows := firstEng.Size()
	t := &Tabs{
		open: open,
		cols: cols,
		rows: rows,
	}
	first := &tab{ctrl: firstCtrl, eng: firstEng, closer: firstCloser}
	t.tabs = append(t.tabs, first)
	t.startPump(first)
	return t
}

// startPump copies a tab's session output into its engine until the session
// ends. Each tab pumps independently so unselected tabs keep updating. This
// mirrors the single-session pump the frontends use; in tab mode the frontend
// defers pumping to here and only reads engine snapshots.
func (t *Tabs) startPump(tb *tab) {
	go func() {
		buf := make([]byte, 32*1024)
		r := tb.ctrl.Stdout()
		for {
			n, err := r.Read(buf)
			if n > 0 {
				tb.eng.Write(buf[:n])
				tb.loaded.Store(true)
			}
			if err != nil {
				return
			}
		}
	}()
}

// Input forwards locally-typed bytes to the active tab's session.
func (t *Tabs) Input(p []byte) error {
	t.mu.Lock()
	a := t.activeTab()
	t.mu.Unlock()
	if a == nil {
		return nil
	}
	return a.ctrl.Input(p)
}

// Resize records the new window size and applies it to every tab's engine and
// remote PTY, so switching to a background tab never shows a stale size.
func (t *Tabs) Resize(cols, rows int) error {
	t.mu.Lock()
	t.cols, t.rows = cols, rows
	tabs := append([]*tab(nil), t.tabs...) // snapshot; resize outside the lock
	t.mu.Unlock()

	var first error
	for _, tb := range tabs {
		tb.eng.Resize(cols, rows)
		if err := tb.ctrl.Resize(cols, rows); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Stdout satisfies render.Controller. In tab mode the frontend renders via
// ActiveEngine and never reads this; the per-tab pumps own the real streams.
// Returning an EOF reader (rather than a live stream) makes any accidental read
// harmless instead of stealing bytes from a pump.
func (t *Tabs) Stdout() io.Reader { return eofReader{} }

// ActiveEngine returns the engine of the selected tab — the grid to draw.
func (t *Tabs) ActiveEngine() terminal.Engine {
	t.mu.Lock()
	defer t.mu.Unlock()
	if a := t.activeTab(); a != nil {
		return a.eng
	}
	return nil
}

// ActiveLoading reports whether the active tab is still awaiting its first
// output byte.
func (t *Tabs) ActiveLoading() bool {
	t.mu.Lock()
	a := t.activeTab()
	t.mu.Unlock()
	return a != nil && !a.loaded.Load()
}

// TabList returns one TabInfo per open tab, numbered by position ("#1", "#2", …)
// with the active tab flagged.
func (t *Tabs) TabList() []render.TabInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]render.TabInfo, len(t.tabs))
	for i := range t.tabs {
		out[i] = render.TabInfo{Title: "#" + strconv.Itoa(i+1), Active: i == t.active}
	}
	return out
}

// NewTab dials an additional session and switches to it. The dial happens
// outside the lock (it can block on the network), so callers running on a UI
// loop should invoke NewTab on its own goroutine.
func (t *Tabs) NewTab() error {
	ctrl, eng, closer, err := t.open()
	if err != nil {
		return err
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		closeTab(closer) // shut down mid-dial; don't leak the new session
		return errClosed
	}
	cols, rows := t.cols, t.rows
	tb := &tab{ctrl: ctrl, eng: eng, closer: closer}
	t.tabs = append(t.tabs, tb)
	t.active = len(t.tabs) - 1
	t.mu.Unlock()

	// Match the new session to the current window before it starts producing.
	eng.Resize(cols, rows)
	_ = ctrl.Resize(cols, rows) // non-fatal: PTY still works at its default size
	t.startPump(tb)
	return nil
}

// CloseActiveTab closes the active session and selects an adjacent tab (the one
// that shifts into its slot, or the new last tab when the rightmost is closed).
// It is a no-op when only one tab remains, so there is always a session to
// render and drive.
func (t *Tabs) CloseActiveTab() {
	t.mu.Lock()
	if len(t.tabs) <= 1 {
		t.mu.Unlock()
		return
	}
	idx := t.active
	victim := t.tabs[idx]
	t.tabs = append(t.tabs[:idx], t.tabs[idx+1:]...)
	if t.active >= len(t.tabs) {
		t.active = len(t.tabs) - 1
	}
	t.mu.Unlock()

	closeTab(victim.closer) // its pump exits when the session's reader errors
}

// NextTab moves the selection one tab to the right, wrapping to the first.
func (t *Tabs) NextTab() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.tabs) > 0 {
		t.active = (t.active + 1) % len(t.tabs)
	}
}

// PrevTab moves the selection one tab to the left, wrapping to the last.
func (t *Tabs) PrevTab() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n := len(t.tabs); n > 0 {
		t.active = (t.active - 1 + n) % n
	}
}

// Close tears down every tab. It is idempotent. Each pump goroutine exits on its
// own once its session's reader returns an error.
func (t *Tabs) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	tabs := t.tabs
	t.tabs = nil
	t.mu.Unlock()

	var first error
	for _, tb := range tabs {
		if err := closeTab(tb.closer); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// activeTab returns the selected tab, or nil if there are none. Caller holds mu.
func (t *Tabs) activeTab() *tab {
	if t.active < 0 || t.active >= len(t.tabs) {
		return nil
	}
	return t.tabs[t.active]
}

func closeTab(closer func() error) error {
	if closer == nil {
		return nil
	}
	return closer()
}

// eofReader is an always-empty io.Reader (see Tabs.Stdout).
type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

// Compile-time check that Tabs is a full render.TabController.
var _ render.TabController = (*Tabs)(nil)
