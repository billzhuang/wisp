// Package terminal defines the terminal engine seam used by wisp.
//
// The Engine interface is the boundary between "bytes arriving from the shell's
// PTY" and "a grid of cells a renderer can draw". The default implementation
// (vtEngine, see vt.go) is a pure-Go VT parser so the whole pipeline is
// buildable and testable without cgo. The libghostty-backed engine lives in
// libghostty.go behind the `libghostty` build tag; it satisfies the same
// interface so the rest of the program never changes.
package terminal

import "image/color"

// Engine is a terminal emulator: it consumes a byte stream produced by a
// program (escape sequences + text) and maintains a cell grid that a renderer
// can read each frame.
//
// Engine is an io.Writer so a session's stdout can be copied straight into it
// with io.Copy.
type Engine interface {
	// Write feeds bytes from the shell's PTY into the parser. It never returns
	// a short write or error for well-formed terminal output; partial escape
	// sequences are buffered until completed by a later Write.
	Write(p []byte) (int, error)

	// Resize changes the grid dimensions, e.g. on window resize. Content is
	// preserved where it still fits.
	Resize(cols, rows int)

	// Size reports the current grid dimensions.
	Size() (cols, rows int)

	// Snapshot returns a copy of the current visible grid. The returned Grid is
	// owned by the caller and safe to read while the engine keeps mutating.
	Snapshot() Grid

	// Cursor returns the current cursor position (0-based col, row) and whether
	// it is visible.
	Cursor() (col, row int, visible bool)
}

// Attr is a bitset of character rendition attributes.
type Attr uint8

const (
	AttrBold Attr = 1 << iota
	AttrItalic
	AttrUnderline
	AttrReverse
)

// Cell is a single grid position: one rune plus its rendition.
type Cell struct {
	Rune rune
	FG   color.Color // nil means default foreground
	BG   color.Color // nil means default background
	Attr Attr
}

// Blank is the empty cell.
var Blank = Cell{Rune: ' '}

// Grid is a snapshot of the visible screen: Rows rows of Cols cells, row-major.
type Grid struct {
	Cols, Rows int
	Cells      []Cell // len == Cols*Rows, indexed [row*Cols+col]
}

// At returns the cell at (col, row). Out-of-range coordinates return Blank.
func (g Grid) At(col, row int) Cell {
	if col < 0 || row < 0 || col >= g.Cols || row >= g.Rows {
		return Blank
	}
	return g.Cells[row*g.Cols+col]
}

// Line returns the text of a single row with trailing blanks trimmed. It is a
// convenience for tests and the plain-text formatter.
func (g Grid) Line(row int) string {
	if row < 0 || row >= g.Rows {
		return ""
	}
	runes := make([]rune, g.Cols)
	last := -1
	for col := 0; col < g.Cols; col++ {
		r := g.Cells[row*g.Cols+col].Rune
		if r == 0 {
			r = ' '
		}
		runes[col] = r
		if r != ' ' {
			last = col
		}
	}
	return string(runes[:last+1])
}

// String renders the whole grid as plain text, one line per row, trailing blank
// lines trimmed. Useful for tests and `--format text` style output.
func (g Grid) String() string {
	lastRow := -1
	lines := make([]string, g.Rows)
	for row := 0; row < g.Rows; row++ {
		lines[row] = g.Line(row)
		if lines[row] != "" {
			lastRow = row
		}
	}
	out := ""
	for row := 0; row <= lastRow; row++ {
		out += lines[row]
		if row < lastRow {
			out += "\n"
		}
	}
	return out
}
