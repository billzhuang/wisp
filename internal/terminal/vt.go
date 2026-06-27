package terminal

import (
	"sync"

	"github.com/billzhuang/wisp/internal/terminal/palette"
)

// vtEngine is a pure-Go terminal emulator implementing a practical subset of the
// xterm/VT100 escape repertoire: printable text (UTF-8), the common C0 control
// codes, CSI cursor movement, erase-in-line / erase-in-display, and SGR colour
// and attribute selection (including the 16-colour, 256-colour and truecolour
// forms). It is deliberately renderer-agnostic and fully deterministic so the
// rest of wisp can be built and tested without cgo or libghostty.
//
// It is safe for concurrent Write (from the SSH read pump) and Snapshot/Cursor
// (from a render loop): all state is guarded by mu.
type vtEngine struct {
	mu sync.Mutex

	cols, rows int
	// grid holds one slice per visible row. Storing rows as separate slices lets
	// scrollUp/scrollDown rotate row references (O(rows)) and recycle one row,
	// instead of memmoving the whole cell array (O(rows*cols)) on every line feed.
	grid [][]Cell

	curCol, curRow int
	curVisible     bool

	cur Cell // current pen: Attr + FG/BG applied to printed runes

	// escape-sequence parser state
	state    parseState
	params   []int // collected CSI numeric parameters
	curParam int
	hasParam bool
	private  byte // CSI private marker, e.g. '?'
	utf      utf8acc
}

type parseState int

const (
	stateGround parseState = iota
	stateEsc
	stateCSI
)

// NewTerminal creates a pure-Go terminal engine. Options configure the initial
// grid size; the default is 80x24.
func NewTerminal(opts ...Option) Engine {
	cfg := config{cols: 80, rows: 24}
	for _, o := range opts {
		o(&cfg)
	}
	e := &vtEngine{
		cols:       cfg.cols,
		rows:       cfg.rows,
		curVisible: true,
		cur:        Blank,
	}
	e.grid = newGrid(cfg.cols, cfg.rows)
	return e
}

type config struct{ cols, rows int }

// Option configures a new terminal engine.
type Option func(*config)

// WithSize sets the initial grid dimensions.
func WithSize(cols, rows int) Option {
	return func(c *config) {
		if cols > 0 {
			c.cols = cols
		}
		if rows > 0 {
			c.rows = rows
		}
	}
}

func newGrid(cols, rows int) [][]Cell {
	g := make([][]Cell, rows)
	for r := range g {
		g[r] = makeRow(cols)
	}
	return g
}

func makeRow(cols int) []Cell {
	row := make([]Cell, cols)
	blankRow(row)
	return row
}

// blankRow resets every cell in row to Blank.
func blankRow(row []Cell) {
	for i := range row {
		row[i] = Blank
	}
}

func (e *vtEngine) Size() (int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cols, e.rows
}

func (e *vtEngine) Cursor() (int, int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.curCol, e.curRow, e.curVisible
}

func (e *vtEngine) Snapshot() Grid {
	e.mu.Lock()
	defer e.mu.Unlock()
	g := Grid{Cols: e.cols, Rows: e.rows, Cells: make([]Cell, e.cols*e.rows)}
	for r := 0; r < e.rows; r++ {
		copy(g.Cells[r*e.cols:], e.grid[r])
	}
	return g
}

func (e *vtEngine) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	next := newGrid(cols, rows)
	// Preserve overlapping content (top-left aligned).
	overlap := min(cols, e.cols)
	for r := 0; r < rows && r < e.rows; r++ {
		copy(next[r], e.grid[r][:overlap])
	}
	e.cols, e.rows, e.grid = cols, rows, next
	e.curCol = clamp(e.curCol, 0, cols-1)
	e.curRow = clamp(e.curRow, 0, rows-1)
}

func (e *vtEngine) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, b := range p {
		e.step(b)
	}
	return len(p), nil
}

// step advances the parser by one byte.
func (e *vtEngine) step(b byte) {
	switch e.state {
	case stateGround:
		e.ground(b)
	case stateEsc:
		e.esc(b)
	case stateCSI:
		e.csi(b)
	}
}

func (e *vtEngine) ground(b byte) {
	// Continue a multi-byte UTF-8 sequence if one is in progress.
	if e.utf.pending() {
		if r, ok := e.utf.feed(b); ok {
			e.put(r)
			// A byte that broke the sequence (e.g. ESC) must be reprocessed as
			// fresh input rather than swallowed.
			if pb, has := e.utf.takePending(); has {
				e.step(pb)
			}
		}
		return
	}
	switch {
	case b == 0x1b: // ESC
		e.state = stateEsc
	case b < 0x20: // C0 control
		e.control(b)
	case b < 0x80: // printable ASCII
		e.put(rune(b))
	default: // start of UTF-8 multibyte
		if r, ok := e.utf.feed(b); ok {
			e.put(r)
		}
	}
}

func (e *vtEngine) control(b byte) {
	switch b {
	case '\n': // LF
		e.lineFeed()
	case '\r': // CR
		e.curCol = 0
	case '\b': // BS
		if e.curCol > 0 {
			e.curCol--
		}
	case '\t': // HT — advance to next 8-column tab stop
		e.curCol = min((e.curCol/8+1)*8, e.cols-1)
	case '\a': // BEL — ignored
	}
}

func (e *vtEngine) esc(b byte) {
	switch b {
	case '[': // CSI
		e.state = stateCSI
		e.params = e.params[:0]
		e.curParam = 0
		e.hasParam = false
		e.private = 0
	case 'c': // RIS — full reset
		e.reset()
		e.state = stateGround
	case 'M': // RI — reverse index (cursor up, scroll if at top)
		if e.curRow == 0 {
			e.scrollDown()
		} else {
			e.curRow--
		}
		e.state = stateGround
	case 'D': // IND — index (cursor down, scroll if at bottom)
		e.lineFeed()
		e.state = stateGround
	case 'E': // NEL — next line
		e.curCol = 0
		e.lineFeed()
		e.state = stateGround
	default:
		// Unsupported / ignored escape (e.g. charset selection); drop it.
		e.state = stateGround
	}
}

func (e *vtEngine) csi(b byte) {
	switch {
	case b >= '0' && b <= '9':
		e.curParam = e.curParam*10 + int(b-'0')
		e.hasParam = true
	case b == ';':
		e.params = append(e.params, e.curParam)
		e.curParam = 0
		e.hasParam = false
	case b == '?' || b == '>' || b == '<' || b == '=':
		e.private = b
	case b >= 0x40 && b <= 0x7e: // final byte
		if e.hasParam || len(e.params) > 0 {
			e.params = append(e.params, e.curParam)
		}
		e.dispatchCSI(b)
		e.state = stateGround
	}
}

// param returns the i-th CSI parameter, or def if absent/zero-as-default.
func (e *vtEngine) param(i, def int) int {
	if i >= len(e.params) || e.params[i] == 0 {
		return def
	}
	return e.params[i]
}

func (e *vtEngine) dispatchCSI(final byte) {
	switch final {
	case 'A': // CUU — cursor up
		e.curRow = clamp(e.curRow-e.param(0, 1), 0, e.rows-1)
	case 'B': // CUD — cursor down
		e.curRow = clamp(e.curRow+e.param(0, 1), 0, e.rows-1)
	case 'C': // CUF — cursor forward
		e.curCol = clamp(e.curCol+e.param(0, 1), 0, e.cols-1)
	case 'D': // CUB — cursor back
		e.curCol = clamp(e.curCol-e.param(0, 1), 0, e.cols-1)
	case 'E': // CNL — cursor next line
		e.curRow = clamp(e.curRow+e.param(0, 1), 0, e.rows-1)
		e.curCol = 0
	case 'F': // CPL — cursor previous line
		e.curRow = clamp(e.curRow-e.param(0, 1), 0, e.rows-1)
		e.curCol = 0
	case 'G', '`': // CHA / HPA — cursor horizontal absolute (1-based)
		e.curCol = clamp(e.param(0, 1)-1, 0, e.cols-1)
	case 'd': // VPA — line position absolute (1-based)
		e.curRow = clamp(e.param(0, 1)-1, 0, e.rows-1)
	case 'H', 'f': // CUP / HVP — cursor position (1-based row;col)
		e.curRow = clamp(e.param(0, 1)-1, 0, e.rows-1)
		e.curCol = clamp(e.param(1, 1)-1, 0, e.cols-1)
	case 'J': // ED — erase in display
		e.eraseDisplay(e.param(0, 0))
	case 'K': // EL — erase in line
		e.eraseLine(e.param(0, 0))
	case 'm': // SGR — select graphic rendition
		e.sgr()
	case 'h', 'l': // SM/RM — set/reset mode (we handle cursor visibility)
		if e.private == '?' {
			for _, p := range e.params {
				if p == 25 { // DECTCEM
					e.curVisible = final == 'h'
				}
			}
		}
	}
}

func (e *vtEngine) sgr() {
	if len(e.params) == 0 {
		e.cur = Blank
		return
	}
	for i := 0; i < len(e.params); i++ {
		p := e.params[i]
		switch {
		case p == 0:
			e.cur = Blank
		case p == 1:
			e.cur.Attr |= AttrBold
		case p == 3:
			e.cur.Attr |= AttrItalic
		case p == 4:
			e.cur.Attr |= AttrUnderline
		case p == 7:
			e.cur.Attr |= AttrReverse
		case p == 22:
			e.cur.Attr &^= AttrBold
		case p == 23:
			e.cur.Attr &^= AttrItalic
		case p == 24:
			e.cur.Attr &^= AttrUnderline
		case p == 27:
			e.cur.Attr &^= AttrReverse
		case p >= 30 && p <= 37:
			e.cur.FG = palette.ANSI16(p - 30)
		case p == 39:
			e.cur.FG = nil
		case p >= 40 && p <= 47:
			e.cur.BG = palette.ANSI16(p - 40)
		case p == 49:
			e.cur.BG = nil
		case p >= 90 && p <= 97:
			e.cur.FG = palette.ANSI16(p - 90 + 8)
		case p >= 100 && p <= 107:
			e.cur.BG = palette.ANSI16(p - 100 + 8)
		case p == 38 || p == 48:
			// Extended colour: 38;5;n (256) or 38;2;r;g;b (truecolour).
			col, consumed := palette.Extended(e.params[i:])
			if consumed == 0 {
				return // malformed; stop parsing the rest
			}
			if p == 38 {
				e.cur.FG = col
			} else {
				e.cur.BG = col
			}
			i += consumed - 1
		}
	}
}

func (e *vtEngine) put(r rune) {
	if e.curCol >= e.cols {
		// Wrap to next line.
		e.curCol = 0
		e.lineFeed()
	}
	c := e.cur
	c.Rune = r
	e.grid[e.curRow][e.curCol] = c
	e.curCol++
}

func (e *vtEngine) lineFeed() {
	if e.curRow >= e.rows-1 {
		e.scrollUp()
	} else {
		e.curRow++
	}
}

// scrollUp moves every row up by one, recycling the top row as a cleared bottom
// row. Only row references are shifted, so the cost is O(rows) plus one row
// clear — not an O(rows*cols) copy of the whole grid.
func (e *vtEngine) scrollUp() {
	recycled := e.grid[0]
	copy(e.grid, e.grid[1:]) // shift rows 1..n-1 down into 0..n-2
	blankRow(recycled)
	e.grid[e.rows-1] = recycled
}

// scrollDown is the mirror of scrollUp: every row moves down by one and the
// bottom row is recycled as a cleared top row.
func (e *vtEngine) scrollDown() {
	recycled := e.grid[e.rows-1]
	copy(e.grid[1:], e.grid[:e.rows-1]) // shift rows 0..n-2 up into 1..n-1
	blankRow(recycled)
	e.grid[0] = recycled
}

func (e *vtEngine) eraseLine(mode int) {
	row := e.grid[e.curRow]
	switch mode {
	case 0: // cursor to end of line
		for c := e.curCol; c < e.cols; c++ {
			row[c] = Blank
		}
	case 1: // start of line to cursor
		for c := 0; c <= e.curCol && c < e.cols; c++ {
			row[c] = Blank
		}
	case 2: // whole line
		blankRow(row)
	}
}

func (e *vtEngine) eraseDisplay(mode int) {
	switch mode {
	case 0: // cursor to end of screen
		e.eraseLine(0)
		for r := e.curRow + 1; r < e.rows; r++ {
			blankRow(e.grid[r])
		}
	case 1: // start of screen to cursor
		for r := 0; r < e.curRow; r++ {
			blankRow(e.grid[r])
		}
		e.eraseLine(1)
	case 2, 3: // whole screen
		for r := 0; r < e.rows; r++ {
			blankRow(e.grid[r])
		}
	}
}

func (e *vtEngine) reset() {
	e.grid = newGrid(e.cols, e.rows)
	e.curCol, e.curRow = 0, 0
	e.curVisible = true
	e.cur = Blank
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
