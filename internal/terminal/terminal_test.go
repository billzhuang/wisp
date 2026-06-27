// Tests for terminal.go (Grid, Cell, Attr, Blank), utf8.go (utf8acc), and
// engine_default.go (DefaultEngine, Backend). Kept in the `terminal` package so
// unexported types (utf8acc) are accessible without cgo.
package terminal

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Grid.At
// ---------------------------------------------------------------------------

func makeGrid(cols, rows int) Grid {
	cells := make([]Cell, cols*rows)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			cells[r*cols+c] = Cell{Rune: rune('a' + c + r*cols)}
		}
	}
	return Grid{Cols: cols, Rows: rows, Cells: cells}
}

func TestGridAtInBounds(t *testing.T) {
	g := makeGrid(3, 2)
	// First cell should be 'a' (0+0*3=0)
	if got := g.At(0, 0).Rune; got != 'a' {
		t.Errorf("At(0,0) = %q, want 'a'", got)
	}
	// Cell at (2,1) => index 1*3+2 = 5 => 'a'+5 = 'f'
	if got := g.At(2, 1).Rune; got != 'f' {
		t.Errorf("At(2,1) = %q, want 'f'", got)
	}
}

func TestGridAtNegativeCol(t *testing.T) {
	g := makeGrid(3, 2)
	cell := g.At(-1, 0)
	if cell != Blank {
		t.Errorf("At(-1,0) = %+v, want Blank", cell)
	}
}

func TestGridAtNegativeRow(t *testing.T) {
	g := makeGrid(3, 2)
	cell := g.At(0, -1)
	if cell != Blank {
		t.Errorf("At(0,-1) = %+v, want Blank", cell)
	}
}

func TestGridAtColEqualsCols(t *testing.T) {
	g := makeGrid(3, 2)
	cell := g.At(3, 0) // col == Cols, out-of-range
	if cell != Blank {
		t.Errorf("At(3,0) = %+v, want Blank", cell)
	}
}

func TestGridAtRowEqualsRows(t *testing.T) {
	g := makeGrid(3, 2)
	cell := g.At(0, 2) // row == Rows, out-of-range
	if cell != Blank {
		t.Errorf("At(0,2) = %+v, want Blank", cell)
	}
}

func TestGridAtColFarOutOfRange(t *testing.T) {
	g := makeGrid(5, 3)
	if cell := g.At(1000, 0); cell != Blank {
		t.Errorf("At(1000,0) = %+v, want Blank", cell)
	}
}

func TestGridAtRowFarOutOfRange(t *testing.T) {
	g := makeGrid(5, 3)
	if cell := g.At(0, 1000); cell != Blank {
		t.Errorf("At(0,1000) = %+v, want Blank", cell)
	}
}

// ---------------------------------------------------------------------------
// Grid.Line
// ---------------------------------------------------------------------------

func TestGridLineNegativeRow(t *testing.T) {
	g := makeGrid(5, 2)
	if got := g.Line(-1); got != "" {
		t.Errorf("Line(-1) = %q, want empty", got)
	}
}

func TestGridLineRowEqualsRows(t *testing.T) {
	g := makeGrid(5, 2)
	if got := g.Line(2); got != "" {
		t.Errorf("Line(2) = %q, want empty (out of range)", got)
	}
}

func TestGridLineTrimsTrailingBlanks(t *testing.T) {
	// Build a 5-col grid with only "hi" in col 0-1, rest spaces.
	cells := make([]Cell, 5)
	cells[0] = Cell{Rune: 'h'}
	cells[1] = Cell{Rune: 'i'}
	// cells 2-4 have zero Rune (treated as space by Line)
	g := Grid{Cols: 5, Rows: 1, Cells: cells}
	got := g.Line(0)
	if got != "hi" {
		t.Errorf("Line(0) = %q, want %q", got, "hi")
	}
}

func TestGridLineAllBlanks(t *testing.T) {
	cells := make([]Cell, 5) // all zero-value Cells
	g := Grid{Cols: 5, Rows: 1, Cells: cells}
	if got := g.Line(0); got != "" {
		t.Errorf("Line(0) of all-blank row = %q, want empty", got)
	}
}

func TestGridLineFullContent(t *testing.T) {
	cells := []Cell{
		{Rune: 'a'}, {Rune: 'b'}, {Rune: 'c'},
	}
	g := Grid{Cols: 3, Rows: 1, Cells: cells}
	if got := g.Line(0); got != "abc" {
		t.Errorf("Line(0) = %q, want abc", got)
	}
}

// ---------------------------------------------------------------------------
// Grid.String
// ---------------------------------------------------------------------------

func TestGridStringEmpty(t *testing.T) {
	g := Grid{Cols: 5, Rows: 3, Cells: make([]Cell, 15)}
	if s := g.String(); s != "" {
		t.Errorf("String() of blank grid = %q, want empty", s)
	}
}

func TestGridStringSingleRow(t *testing.T) {
	cells := []Cell{{Rune: 'x'}, {Rune: 'y'}}
	g := Grid{Cols: 2, Rows: 1, Cells: cells}
	if got := g.String(); got != "xy" {
		t.Errorf("String() = %q, want xy", got)
	}
}

func TestGridStringMultipleRows(t *testing.T) {
	// 3-col, 3-row grid: row0="abc", row1="def", row2=blank
	cells := make([]Cell, 9)
	cells[0] = Cell{Rune: 'a'}
	cells[1] = Cell{Rune: 'b'}
	cells[2] = Cell{Rune: 'c'}
	cells[3] = Cell{Rune: 'd'}
	cells[4] = Cell{Rune: 'e'}
	cells[5] = Cell{Rune: 'f'}
	// row2 is zero → blank → trimmed by String
	g := Grid{Cols: 3, Rows: 3, Cells: cells}
	got := g.String()
	want := "abc\ndef"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestGridStringTrimsTrailingBlankRows(t *testing.T) {
	// row0 has content; row1-2 are blank — String must omit row1-2 entirely.
	cells := make([]Cell, 15) // 5 cols × 3 rows
	cells[0] = Cell{Rune: 'z'}
	g := Grid{Cols: 5, Rows: 3, Cells: cells}
	got := g.String()
	if got != "z" {
		t.Errorf("String() = %q, want z", got)
	}
}

func TestGridStringIntermediateBlankRow(t *testing.T) {
	// row0="a", row1=blank, row2="b" — blank row in middle must be preserved.
	cells := make([]Cell, 9) // 3 cols × 3 rows
	cells[0] = Cell{Rune: 'a'}
	cells[6] = Cell{Rune: 'b'}
	g := Grid{Cols: 3, Rows: 3, Cells: cells}
	got := g.String()
	if !strings.Contains(got, "\n") {
		t.Errorf("String() = %q, expected newlines between rows", got)
	}
	lines := strings.Split(got, "\n")
	if lines[0] != "a" || lines[2] != "b" {
		t.Errorf("String() lines = %q", lines)
	}
}

// ---------------------------------------------------------------------------
// Blank
// ---------------------------------------------------------------------------

func TestBlankIsSpace(t *testing.T) {
	if Blank.Rune != ' ' {
		t.Errorf("Blank.Rune = %q, want ' '", Blank.Rune)
	}
	if Blank.FG != nil {
		t.Errorf("Blank.FG should be nil")
	}
	if Blank.BG != nil {
		t.Errorf("Blank.BG should be nil")
	}
	if Blank.Attr != 0 {
		t.Errorf("Blank.Attr should be 0")
	}
}

// ---------------------------------------------------------------------------
// Attr constants
// ---------------------------------------------------------------------------

func TestAttrConstantsAreDistinct(t *testing.T) {
	attrs := []Attr{AttrBold, AttrItalic, AttrUnderline, AttrReverse}
	for i, a := range attrs {
		for j, b := range attrs {
			if i != j && a == b {
				t.Errorf("Attr constants %d and %d have the same value %d", i, j, a)
			}
		}
	}
}

func TestAttrConstantsArePowersOfTwo(t *testing.T) {
	for _, a := range []Attr{AttrBold, AttrItalic, AttrUnderline, AttrReverse} {
		if a == 0 || (a&(a-1)) != 0 {
			t.Errorf("Attr %d is not a power of two", a)
		}
	}
}

func TestAttrCombination(t *testing.T) {
	combined := AttrBold | AttrItalic
	if combined&AttrBold == 0 {
		t.Error("combined & AttrBold == 0")
	}
	if combined&AttrItalic == 0 {
		t.Error("combined & AttrItalic == 0")
	}
	if combined&AttrUnderline != 0 {
		t.Error("combined & AttrUnderline should be 0")
	}
}

func TestAttrBoldValue(t *testing.T) {
	if AttrBold != 1 {
		t.Errorf("AttrBold = %d, want 1", AttrBold)
	}
}

// ---------------------------------------------------------------------------
// utf8acc
// ---------------------------------------------------------------------------

func TestUtf8AccASCII(t *testing.T) {
	var acc utf8acc
	r, ok := acc.feed('A')
	if !ok || r != 'A' {
		t.Errorf("feed('A') = (%q, %v), want ('A', true)", r, ok)
	}
	if acc.pending() {
		t.Error("pending() should be false after ASCII byte")
	}
}

func TestUtf8AccASCIIZero(t *testing.T) {
	var acc utf8acc
	r, ok := acc.feed(0x00)
	if !ok || r != 0 {
		t.Errorf("feed(0x00) = (%q, %v), want (0, true)", r, ok)
	}
}

func TestUtf8Acc2ByteSequence(t *testing.T) {
	// 'é' = U+00E9 = 0xC3 0xA9
	var acc utf8acc
	r, ok := acc.feed(0xc3)
	if ok {
		t.Errorf("feed(0xc3) = ok=true, want false (waiting for continuation)")
	}
	if !acc.pending() {
		t.Error("pending() should be true after 2-byte leader")
	}
	r, ok = acc.feed(0xa9)
	if !ok {
		t.Errorf("feed(0xa9) = ok=false, want true (complete rune)")
	}
	if r != 'é' {
		t.Errorf("decoded rune = %q, want 'é'", r)
	}
	if acc.pending() {
		t.Error("pending() should be false after complete rune")
	}
}

func TestUtf8Acc3ByteSequence(t *testing.T) {
	// '€' = U+20AC = 0xE2 0x82 0xAC
	var acc utf8acc
	acc.feed(0xe2)
	acc.feed(0x82)
	r, ok := acc.feed(0xac)
	if !ok || r != '€' {
		t.Errorf("3-byte decode = (%q, %v), want ('€', true)", r, ok)
	}
}

func TestUtf8Acc4ByteSequence(t *testing.T) {
	// '😀' = U+1F600 = 0xF0 0x9F 0x98 0x80
	var acc utf8acc
	acc.feed(0xf0)
	acc.feed(0x9f)
	acc.feed(0x98)
	r, ok := acc.feed(0x80)
	if !ok || r != '😀' {
		t.Errorf("4-byte decode = (%q, %v), want ('😀', true)", r, ok)
	}
}

func TestUtf8AccInvalidLeadByte(t *testing.T) {
	// 0xFF is not a valid UTF-8 leading byte.
	var acc utf8acc
	r, ok := acc.feed(0xff)
	if !ok {
		t.Error("feed(0xFF) should return immediately (ok=true)")
	}
	if r != utf8.RuneError {
		t.Errorf("feed(0xFF) = %q, want RuneError", r)
	}
}

func TestUtf8AccInvalidContinuationByte(t *testing.T) {
	// Start a 2-byte sequence, then feed a non-continuation byte.
	var acc utf8acc
	acc.feed(0xc3) // 2-byte leader
	r, ok := acc.feed(0x41) // 'A', not a continuation byte (0x80-0xBF)
	if !ok {
		t.Error("malformed continuation should emit RuneError immediately (ok=true)")
	}
	if r != utf8.RuneError {
		t.Errorf("got %q, want RuneError", r)
	}
	// After reset, the accumulator should accept normal bytes again.
	r2, ok2 := acc.feed('X')
	if !ok2 || r2 != 'X' {
		t.Errorf("after reset, feed('X') = (%q, %v), want ('X', true)", r2, ok2)
	}
}

func TestUtf8AccPendingFalseInitially(t *testing.T) {
	var acc utf8acc
	if acc.pending() {
		t.Error("fresh utf8acc should not be pending")
	}
}

func TestUtf8AccFeedAllASCII(t *testing.T) {
	var acc utf8acc
	input := "hello, world!"
	for _, b := range []byte(input) {
		r, ok := acc.feed(b)
		if !ok {
			t.Fatalf("ASCII byte 0x%02x returned ok=false", b)
		}
		if r != rune(b) {
			t.Fatalf("ASCII byte 0x%02x returned rune %q", b, r)
		}
	}
}

// ---------------------------------------------------------------------------
// DefaultEngine + Backend (engine_default.go)
// ---------------------------------------------------------------------------

func TestDefaultEngineReturnsEngine(t *testing.T) {
	eng := DefaultEngine(40, 20)
	if eng == nil {
		t.Fatal("DefaultEngine returned nil")
	}
	cols, rows := eng.Size()
	if cols != 40 || rows != 20 {
		t.Errorf("DefaultEngine(40,20).Size() = %d×%d, want 40×20", cols, rows)
	}
}

func TestDefaultEngineIsWritable(t *testing.T) {
	eng := DefaultEngine(10, 5)
	n, err := eng.Write([]byte("test"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 4 {
		t.Errorf("Write returned n=%d, want 4", n)
	}
	g := eng.Snapshot()
	if g.Line(0) != "test" {
		t.Errorf("Snapshot().Line(0) = %q, want test", g.Line(0))
	}
}

func TestDefaultEngineCanResize(t *testing.T) {
	eng := DefaultEngine(10, 5)
	eng.Resize(20, 10)
	cols, rows := eng.Size()
	if cols != 20 || rows != 10 {
		t.Errorf("after Resize(20,10), Size() = %d×%d", cols, rows)
	}
}

func TestDefaultEngineSnapshot(t *testing.T) {
	eng := DefaultEngine(5, 3)
	g := eng.Snapshot()
	if g.Cols != 5 || g.Rows != 3 {
		t.Errorf("Snapshot dims = %d×%d, want 5×3", g.Cols, g.Rows)
	}
}

func TestBackendConstantNonEmpty(t *testing.T) {
	if Backend == "" {
		t.Error("Backend constant must not be empty")
	}
}

func TestBackendConstantContainsPureGo(t *testing.T) {
	// The default build (no libghostty tag) should identify itself as pure-go.
	if !strings.Contains(Backend, "pure-go") {
		t.Errorf("Backend = %q, expected to contain 'pure-go'", Backend)
	}
}