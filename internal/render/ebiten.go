//go:build ebiten

// This file is the Ebitengine GPU frontend, compiled only with `-tags ebiten`.
// It draws the terminal.Engine cell grid to a window and forwards keyboard
// input to the shell. Building it requires the platform GPU/windowing
// toolchain (on Linux: libgl1-mesa-dev, libxrandr-dev, libxcursor-dev,
// libxinerama-dev, libxi-dev; macOS: the system frameworks), which is why it is
// kept behind a tag and the default build uses the stdio frontend instead.
//
// Rendering follows gostty's approach: a fixed-cell monospace grid. The bundled
// basicfont keeps this self-contained (no external font asset); swap in a
// shaped font face for wide-char / ligature support as a later polish step.

package render

import (
	"context"
	"image/color"
	"sync"

	"github.com/billzhuang/wisp/internal/banner"
	"github.com/billzhuang/wisp/internal/terminal"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/basicfont"
)

const (
	cellW = 7  // basicfont.Face7x13 advance
	cellH = 13 // basicfont.Face7x13 height

	// tabBarW is the width (px) of the vertical tab strip drawn down the left
	// edge in multi-tab mode; tabRowH is the height of one entry in it.
	tabBarW = cellW * 5 // room for "#NN" plus padding
	tabRowH = cellH + 4
)

// NewDefault returns the Ebitengine frontend under the `ebiten` build tag.
func NewDefault() Frontend { return &ebitenFrontend{} }

type ebitenFrontend struct {
	ctrl Controller
	tabs TabController // non-nil when the controller supports tabs
	eng  terminal.Engine
	face *text.GoXFace
	cols int
	rows int

	mu       sync.Mutex
	banner   string       // update notice; empty hides the banner
	install  func() error // click-to-install action
	updating bool         // an install is in flight
	loading  bool         // true until the first shell byte arrives
}

// SetUpdate implements render.UpdatePrompter: it shows a top banner offering
// the update and arms Ctrl+U to install it.
func (f *ebitenFrontend) SetUpdate(notice string, install func() error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.banner = notice
	f.install = install
}

func (f *ebitenFrontend) Run(ctx context.Context, ctrl Controller, eng terminal.Engine) error {
	f.ctrl = ctrl
	f.eng = eng
	f.face = text.NewGoXFace(basicfont.Face7x13)
	f.cols, f.rows = eng.Size()
	f.loading = true // show the splash until the first shell byte

	if tc, ok := ctrl.(TabController); ok {
		// In tab mode the controller owns a pump per tab; we only snapshot the
		// active tab's engine, and loading state comes from that tab.
		f.tabs = tc
	} else {
		// Single-session fallback: pump shell output into the one engine.
		go func() {
			buf := make([]byte, 32*1024)
			r := ctrl.Stdout()
			for {
				n, err := r.Read(buf)
				if n > 0 {
					eng.Write(buf[:n])
					f.mu.Lock()
					f.loading = false
					f.mu.Unlock()
				}
				if err != nil {
					return
				}
			}
		}()
	}

	ebiten.SetWindowTitle("wisp")
	ebiten.SetWindowSize(f.cols*cellW+f.stripW(), f.rows*cellH)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	return ebiten.RunGame(f)
}

// SupportsTabs implements render.TabCapable: the GUI can present multiple
// sessions as tabs, so main wires a multi-session controller for it.
func (f *ebitenFrontend) SupportsTabs() bool { return true }

// stripW is the tab strip width in pixels: tabBarW in multi-tab mode, else 0.
func (f *ebitenFrontend) stripW() int {
	if f.tabs != nil {
		return tabBarW
	}
	return 0
}

// Update forwards input and propagates window resizes to the engine + shell.
func (f *ebitenFrontend) Update() error {
	ctrlDown := ebiten.IsKeyPressed(ebiten.KeyControlLeft) || ebiten.IsKeyPressed(ebiten.KeyControlRight)

	// Ctrl+U installs a pending update (the click-to-install gesture).
	if ctrlDown && inpututil.IsKeyJustPressed(ebiten.KeyU) {
		f.triggerInstall()
		return nil // swallow the chord; don't forward ^U to the shell
	}

	// Tab-management chords (multi-tab GUI only). Each is swallowed so it never
	// reaches the shell; note they shadow the shell's own Ctrl+T/Ctrl+W.
	if f.tabs != nil && ctrlDown {
		shift := ebiten.IsKeyPressed(ebiten.KeyShiftLeft) || ebiten.IsKeyPressed(ebiten.KeyShiftRight)
		switch {
		case inpututil.IsKeyJustPressed(ebiten.KeyT):
			f.openTab()
			return nil
		case inpututil.IsKeyJustPressed(ebiten.KeyW):
			f.tabs.CloseActiveTab()
			return nil
		case inpututil.IsKeyJustPressed(ebiten.KeyTab):
			if shift {
				f.tabs.PrevTab()
			} else {
				f.tabs.NextTab()
			}
			return nil
		}
	}

	// Printable characters typed this frame.
	if chars := ebiten.AppendInputChars(nil); len(chars) > 0 {
		f.ctrl.Input([]byte(string(chars)))
	}
	// Common control keys.
	if repeatKey(ebiten.KeyEnter) {
		f.ctrl.Input([]byte{'\r'})
	}
	if repeatKey(ebiten.KeyBackspace) {
		f.ctrl.Input([]byte{0x7f})
	}
	if repeatKey(ebiten.KeyTab) {
		f.ctrl.Input([]byte{'\t'})
	}
	if repeatKey(ebiten.KeyEscape) {
		f.ctrl.Input([]byte{0x1b})
	}
	// Arrow keys (xterm normal mode).
	for key, seq := range map[ebiten.Key]string{
		ebiten.KeyArrowUp:    "\x1b[A",
		ebiten.KeyArrowDown:  "\x1b[B",
		ebiten.KeyArrowRight: "\x1b[C",
		ebiten.KeyArrowLeft:  "\x1b[D",
	} {
		if repeatKey(key) {
			f.ctrl.Input([]byte(seq))
		}
	}
	return nil
}

// repeatKey reports an initial press plus auto-repeat.
func repeatKey(k ebiten.Key) bool {
	d := inpututil.KeyPressDuration(k)
	return d == 1 || (d > 20 && d%3 == 0)
}

// triggerInstall runs the armed install action once, in the background, and
// reflects progress in the banner.
func (f *ebitenFrontend) triggerInstall() {
	f.mu.Lock()
	if f.install == nil || f.updating {
		f.mu.Unlock()
		return
	}
	install := f.install
	f.updating = true
	f.banner = "Installing update…"
	f.mu.Unlock()

	go func() {
		err := install()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.updating = false
		if err != nil {
			f.banner = "Update failed: " + err.Error()
			return
		}
		f.banner = "Update installed — restart wisp to apply"
		f.install = nil
	}()
}

// openTab opens a new tab off the UI loop so the window keeps rendering while
// the shell starts; a failure surfaces in the banner.
func (f *ebitenFrontend) openTab() {
	go func() {
		if err := f.tabs.NewTab(); err != nil {
			f.mu.Lock()
			f.banner = "New tab failed: " + err.Error()
			f.mu.Unlock()
		}
	}()
}

// activeEngine is the engine to draw: the active tab's in multi-tab mode, else
// the single-session engine.
func (f *ebitenFrontend) activeEngine() terminal.Engine {
	if f.tabs != nil {
		if e := f.tabs.ActiveEngine(); e != nil {
			return e
		}
	}
	return f.eng
}

// isLoading reports whether to show the connecting splash for the visible tab.
func (f *ebitenFrontend) isLoading() bool {
	if f.tabs != nil {
		return f.tabs.ActiveLoading()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loading
}

func (f *ebitenFrontend) Draw(screen *ebiten.Image) {
	screen.Fill(color.Black)

	if f.isLoading() {
		f.drawSplash(screen)
		f.drawTabs(screen) // keep the strip visible while a tab connects
		return
	}

	strip := f.stripW()
	g := f.activeEngine().Snapshot()
	for row := 0; row < g.Rows; row++ {
		for col := 0; col < g.Cols; col++ {
			c := g.At(col, row)
			if c.Rune == 0 || c.Rune == ' ' {
				continue
			}
			op := &text.DrawOptions{}
			op.GeoM.Translate(float64(strip+col*cellW), float64(row*cellH))
			fg := c.FG
			if fg == nil {
				fg = color.White
			}
			op.ColorScale.ScaleWithColor(fg)
			text.Draw(screen, string(c.Rune), f.face, op)
		}
	}
	f.drawTabs(screen)
	f.drawBanner(screen)
}

// drawTabs paints the vertical tab strip down the left edge: one numbered entry
// per tab (active highlighted) plus a "+" affordance hinting Ctrl+T for a new
// tab. No-op in single-session mode.
func (f *ebitenFrontend) drawTabs(screen *ebiten.Image) {
	if f.tabs == nil {
		return
	}
	tabs := f.tabs.TabList()
	h := screen.Bounds().Dy()

	gutter := ebiten.NewImage(tabBarW, h)
	gutter.Fill(color.RGBA{0x16, 0x16, 0x1e, 0xff}) // dark gutter
	screen.DrawImage(gutter, &ebiten.DrawImageOptions{})

	for i, ti := range tabs {
		y := i * tabRowH
		if ti.Active {
			hl := ebiten.NewImage(tabBarW, tabRowH)
			hl.Fill(color.RGBA{0x2a, 0x2a, 0x3c, 0xff}) // selection
			op := &ebiten.DrawImageOptions{}
			op.GeoM.Translate(0, float64(y))
			screen.DrawImage(hl, op)
		}
		fg := color.Color(color.RGBA{0x9a, 0x9a, 0xb0, 0xff})
		if ti.Active {
			fg = color.White
		}
		op := &text.DrawOptions{}
		op.GeoM.Translate(3, float64(y+(tabRowH-cellH)/2))
		op.ColorScale.ScaleWithColor(fg)
		text.Draw(screen, ti.Title, f.face, op)
	}

	// "+" new-tab hint just below the last tab.
	op := &text.DrawOptions{}
	op.GeoM.Translate(3, float64(len(tabs)*tabRowH+(tabRowH-cellH)/2))
	op.ColorScale.ScaleWithColor(color.RGBA{0x5a, 0x5a, 0x70, 0xff})
	text.Draw(screen, "+", f.face, op)
}

// drawSplash centers the wisp ASCII logo while the session connects.
func (f *ebitenFrontend) drawSplash(screen *ebiten.Image) {
	lines := banner.Lines()
	lines = append(lines, "", "connecting…")
	w := screen.Bounds().Dx()
	startY := screen.Bounds().Dy()/2 - len(lines)*cellH/2
	for i, line := range lines {
		x := w/2 - len(line)*cellW/2
		if x < 0 {
			x = 0
		}
		op := &text.DrawOptions{}
		op.GeoM.Translate(float64(x), float64(startY+i*cellH))
		op.ColorScale.ScaleWithColor(color.RGBA{0x7a, 0xa2, 0xf7, 0xff}) // soft blue
		text.Draw(screen, line, f.face, op)
	}
}

// drawBanner overlays the update notice along the top of the window.
func (f *ebitenFrontend) drawBanner(screen *ebiten.Image) {
	f.mu.Lock()
	msg := f.banner
	f.mu.Unlock()
	if msg == "" {
		return
	}
	w := screen.Bounds().Dx()
	bar := ebiten.NewImage(w, cellH+4)
	bar.Fill(color.RGBA{0x1e, 0x40, 0xff, 0xff}) // blue bar
	screen.DrawImage(bar, &ebiten.DrawImageOptions{})
	op := &text.DrawOptions{}
	op.GeoM.Translate(4, 2)
	op.ColorScale.ScaleWithColor(color.White)
	text.Draw(screen, msg, f.face, op)
}

func (f *ebitenFrontend) Layout(outsideW, outsideH int) (int, int) {
	cols, rows := (outsideW-f.stripW())/cellW, outsideH/cellH
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cols != f.cols || rows != f.rows {
		f.cols, f.rows = cols, rows
		if f.tabs != nil {
			// The TabController resizes every tab's engine and shell PTY.
			f.ctrl.Resize(cols, rows)
		} else {
			f.eng.Resize(cols, rows)
			f.ctrl.Resize(cols, rows)
		}
	}
	return outsideW, outsideH
}
