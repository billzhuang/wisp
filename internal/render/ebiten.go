//go:build ebiten

// This file is the Ebitengine GPU frontend, compiled only with `-tags ebiten`.
// It draws the terminal.Engine cell grid to a window and forwards keyboard
// input to the remote. Building it requires the platform GPU/windowing
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

	"github.com/billzhuang/wisp/internal/terminal"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font/basicfont"
)

const (
	cellW = 7  // basicfont.Face7x13 advance
	cellH = 13 // basicfont.Face7x13 height
)

// NewDefault returns the Ebitengine frontend under the `ebiten` build tag.
func NewDefault() Frontend { return &ebitenFrontend{} }

type ebitenFrontend struct {
	ctrl Controller
	eng  terminal.Engine
	face *text.GoXFace
	cols int
	rows int
}

func (f *ebitenFrontend) Run(ctx context.Context, ctrl Controller, eng terminal.Engine) error {
	f.ctrl = ctrl
	f.eng = eng
	f.face = text.NewGoXFace(basicfont.Face7x13)
	f.cols, f.rows = eng.Size()

	// Pump remote output into the engine in the background; Draw reads snapshots.
	go func() {
		buf := make([]byte, 32*1024)
		r := ctrl.Stdout()
		for {
			n, err := r.Read(buf)
			if n > 0 {
				eng.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	ebiten.SetWindowTitle("wisp")
	ebiten.SetWindowSize(f.cols*cellW, f.rows*cellH)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	return ebiten.RunGame(f)
}

// Update forwards input and propagates window resizes to the engine + remote.
func (f *ebitenFrontend) Update() error {
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

func (f *ebitenFrontend) Draw(screen *ebiten.Image) {
	g := f.eng.Snapshot()
	screen.Fill(color.Black)
	for row := 0; row < g.Rows; row++ {
		for col := 0; col < g.Cols; col++ {
			c := g.At(col, row)
			if c.Rune == 0 || c.Rune == ' ' {
				continue
			}
			op := &text.DrawOptions{}
			op.GeoM.Translate(float64(col*cellW), float64(row*cellH))
			fg := c.FG
			if fg == nil {
				fg = color.White
			}
			op.ColorScale.ScaleWithColor(fg)
			text.Draw(screen, string(c.Rune), f.face, op)
		}
	}
}

func (f *ebitenFrontend) Layout(outsideW, outsideH int) (int, int) {
	cols, rows := outsideW/cellW, outsideH/cellH
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cols != f.cols || rows != f.rows {
		f.cols, f.rows = cols, rows
		f.eng.Resize(cols, rows)
		f.ctrl.Resize(cols, rows)
	}
	return outsideW, outsideH
}
