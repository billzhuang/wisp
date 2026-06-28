//go:build ebiten

package render

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/billzhuang/wisp/internal/terminal"
)

// These tests exercise the tabbed-GUI logic that does not touch graphics
// (Layout math, resize routing, the active-engine/loading fallbacks, and the
// async new-tab path), so they run headlessly under the `ebiten` tag without a
// RunGame loop — mirroring the existing update-banner tests. The strip drawing
// itself (drawTabs) and live key chords are only exercised by the GUI at
// runtime, not here.

// fakeTabs is a render.TabController that records the navigation calls the
// frontend makes.
type fakeTabs struct {
	eng     terminal.Engine
	loading bool
	list    []TabInfo
	newErr  error

	mu       sync.Mutex
	inputs   []byte
	resizes  [][2]int
	newCount int
	closeCnt int
	nextCnt  int
	prevCnt  int
	newCalls chan struct{}
}

func (f *fakeTabs) Stdout() io.Reader { return nil } // unused in tab mode

func (f *fakeTabs) Input(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, p...)
	return nil
}

func (f *fakeTabs) Resize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, [2]int{cols, rows})
	return nil
}

func (f *fakeTabs) ActiveEngine() terminal.Engine { return f.eng }
func (f *fakeTabs) ActiveLoading() bool           { return f.loading }
func (f *fakeTabs) TabList() []TabInfo            { return f.list }

func (f *fakeTabs) NewTab() error {
	f.mu.Lock()
	f.newCount++
	f.mu.Unlock()
	if f.newCalls != nil {
		f.newCalls <- struct{}{}
	}
	return f.newErr
}

func (f *fakeTabs) CloseActiveTab() {
	f.mu.Lock()
	f.closeCnt++
	f.mu.Unlock()
}

func (f *fakeTabs) NextTab() {
	f.mu.Lock()
	f.nextCnt++
	f.mu.Unlock()
}

func (f *fakeTabs) PrevTab() {
	f.mu.Lock()
	f.prevCnt++
	f.mu.Unlock()
}

func (f *fakeTabs) lastResize() ([2]int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.resizes) == 0 {
		return [2]int{}, false
	}
	return f.resizes[len(f.resizes)-1], true
}

func TestFakeTabsSatisfiesInterfaces(t *testing.T) {
	var _ TabController = (*fakeTabs)(nil)
	var _ Controller = (*fakeTabs)(nil)
	var _ TabCapable = (*ebitenFrontend)(nil)
}

func TestStripWidth(t *testing.T) {
	if w := (&ebitenFrontend{}).stripW(); w != 0 {
		t.Fatalf("single-session stripW = %d, want 0", w)
	}
	if w := (&ebitenFrontend{tabs: &fakeTabs{}}).stripW(); w != tabBarW {
		t.Fatalf("tabbed stripW = %d, want %d", w, tabBarW)
	}
}

func TestLayoutReservesTabStripAndRoutesResize(t *testing.T) {
	ft := &fakeTabs{}
	f := &ebitenFrontend{ctrl: ft, tabs: ft}

	const w, h = 700, 260
	f.Layout(w, h)

	wantCols := (w - tabBarW) / cellW
	wantRows := h / cellH
	if f.cols != wantCols || f.rows != wantRows {
		t.Fatalf("grid = %dx%d, want %dx%d (strip reserved)", f.cols, f.rows, wantCols, wantRows)
	}
	if last, ok := ft.lastResize(); !ok || last != [2]int{wantCols, wantRows} {
		t.Fatalf("resize routed to controller = %v (ok=%v), want [%d %d]", last, ok, wantCols, wantRows)
	}
}

func TestLayoutSingleSessionUsesFullWidth(t *testing.T) {
	eng := terminal.DefaultEngine(80, 24)
	fc := &fakeController{}
	f := &ebitenFrontend{ctrl: fc, eng: eng}

	const w, h = 700, 260
	f.Layout(w, h)

	wantCols := w / cellW // no strip reserved
	wantRows := h / cellH
	if f.cols != wantCols || f.rows != wantRows {
		t.Fatalf("grid = %dx%d, want %dx%d (full width)", f.cols, f.rows, wantCols, wantRows)
	}
	if c, r := eng.Size(); c != wantCols || r != wantRows {
		t.Fatalf("engine size = %dx%d, want %dx%d", c, r, wantCols, wantRows)
	}
}

func TestActiveEngineFallsBackToSingleEngine(t *testing.T) {
	single := terminal.DefaultEngine(80, 24)

	// No tabs: the single-session engine is used.
	if got := (&ebitenFrontend{eng: single}).activeEngine(); got != single {
		t.Fatal("without tabs, activeEngine should be the single engine")
	}

	// Tabs present: the active tab's engine is used.
	active := terminal.DefaultEngine(80, 24)
	f := &ebitenFrontend{eng: single, tabs: &fakeTabs{eng: active}}
	if got := f.activeEngine(); got != active {
		t.Fatal("with tabs, activeEngine should be the active tab's engine")
	}

	// Tabs present but no active engine yet: fall back to the single engine.
	f2 := &ebitenFrontend{eng: single, tabs: &fakeTabs{eng: nil}}
	if got := f2.activeEngine(); got != single {
		t.Fatal("with a nil active engine, activeEngine should fall back")
	}
}

func TestIsLoadingTracksActiveTab(t *testing.T) {
	f := &ebitenFrontend{tabs: &fakeTabs{loading: true}}
	if !f.isLoading() {
		t.Fatal("isLoading should reflect the active tab (loading)")
	}
	f.tabs = &fakeTabs{loading: false}
	if f.isLoading() {
		t.Fatal("isLoading should reflect the active tab (loaded)")
	}

	// Single-session mode reads the frontend's own flag.
	single := &ebitenFrontend{}
	single.loading = true
	if !single.isLoading() {
		t.Fatal("single-session isLoading should read f.loading")
	}
}

func TestOpenTabInvokesNewTab(t *testing.T) {
	ft := &fakeTabs{newCalls: make(chan struct{}, 1)}
	f := &ebitenFrontend{tabs: ft}

	f.openTab()
	select {
	case <-ft.newCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("openTab did not call NewTab")
	}

	f.mu.Lock()
	banner := f.banner
	f.mu.Unlock()
	if banner != "" {
		t.Fatalf("banner = %q, want empty on success", banner)
	}
}

func TestOpenTabFailureSurfacesInBanner(t *testing.T) {
	ft := &fakeTabs{newErr: errors.New("boom")}
	f := &ebitenFrontend{tabs: ft}

	f.openTab()
	waitFor(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.banner == "New tab failed: boom"
	})
}
