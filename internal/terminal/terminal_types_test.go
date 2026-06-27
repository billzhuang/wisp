package terminal_test

import (
	"image/color"
	"testing"

	"github.com/billzhuang/wisp/internal/terminal"
)

// makeTestGrid creates a grid filled with the given rune.
func makeTestGrid(cols, rows int, fill rune) terminal.Grid {
	cells := make([]terminal.Cell, cols*rows)
	for i := range cells {
		cells[i] = terminal.Cell{Rune: fill}
	}
	return terminal.Grid{Cols: cols, Rows: rows, Cells: cells}
}

// ---------------------------------------------------------------------------
// Grid.At tests
// ---------------------------------------------------------------------------

func TestGridAtInBounds(t *testing.T) {
	g := makeTestGrid(5, 3, 'X')
	c := g.At(2, 1)
	if c.Rune != 'X' {
		t.Fatalf("At(2,1) = %q, want 'X'", c.Rune)
	}
}

func TestGridAtCorners(t *testing.T) {
	g := terminal.Grid{
		Cols: 3, Rows: 2,
		Cells: []terminal.Cell{
			{Rune: 'A'}, {Rune: 'B'}, {Rune: 'C'},
			{Rune: 'D'}, {Rune: 'E'}, {Rune: 'F'},
		},
	}
	cases := []struct {
		col, row int
		want     rune
	}{
		{0, 0, 'A'},
		{2, 0, 'C'},
		{0, 1, 'D'},
		{2, 1, 'F'},
	}
	for _, tc := range cases {
		c := g.At(tc.col, tc.row)
		if c.Rune != tc.want {
			t.Errorf("At(%d,%d) = %q, want %q", tc.col, tc.row, c.Rune, tc.want)
		}
	}
}

func TestGridAtNegativeColReturnsBlank(t *testing.T) {
	g := makeTestGrid(5, 3, 'X')
	c := g.At(-1, 0)
	if c.Rune != terminal.Blank.Rune {
		t.Fatalf("At(-1,0) = %q, want Blank (%q)", c.Rune, terminal.Blank.Rune)
	}
}

func TestGridAtNegativeRowReturnsBlank(t *testing.T) {
	g := makeTestGrid(5, 3, 'X')
	c := g.At(0, -1)
	if c.Rune != terminal.Blank.Rune {
		t.Fatalf("At(0,-1) = %q, want Blank", c.Rune)
	}
}

func TestGridAtColEqualToColsReturnsBlank(t *testing.T) {
	g := makeTestGrid(5, 3, 'X')
	c := g.At(5, 0) // col == Cols
	if c.Rune != terminal.Blank.Rune {
		t.Fatalf("At(5,0) = %q, want Blank", c.Rune)
	}
}

func TestGridAtColWayOutOfRangeReturnsBlank(t *testing.T) {
	g := makeTestGrid(5, 3, 'X')
	c := g.At(100, 0)
	if c.Rune != terminal.Blank.Rune {
		t.Fatalf("At(100,0) = %q, want Blank", c.Rune)
	}
}

func TestGridAtRowEqualToRowsReturnsBlank(t *testing.T) {
	g := makeTestGrid(5, 3, 'X')
	c := g.At(0, 3) // row == Rows
	if c.Rune != terminal.Blank.Rune {
		t.Fatalf("At(0,3) = %q, want Blank", c.Rune)
	}
}

func TestGridAtRowWayOutOfRangeReturnsBlank(t *testing.T) {
	g := makeTestGrid(5, 3, 'X')
	c := g.At(0, 100)
	if c.Rune != terminal.Blank.Rune {
		t.Fatalf("At(0,100) = %q, want Blank", c.Rune)
	}
}

func TestGridAtEmptyGridReturnsBlank(t *testing.T) {
	g := terminal.Grid{Cols: 0, Rows: 0, Cells: nil}
	c := g.At(0, 0)
	if c.Rune != terminal.Blank.Rune {
		t.Fatalf("At(0,0) on empty grid = %q, want Blank", c.Rune)
	}
}

// ---------------------------------------------------------------------------
// Grid.Line tests
// ---------------------------------------------------------------------------

func TestGridLineBasic(t *testing.T) {
	g := terminal.Grid{
		Cols: 5, Rows: 2,
		Cells: []terminal.Cell{
			{Rune: 'h'}, {Rune: 'i'}, {Rune: ' '}, {Rune: ' '}, {Rune: ' '},
			{Rune: ' '}, {Rune: ' '}, {Rune: ' '}, {Rune: ' '}, {Rune: ' '},
		},
	}
	if got := g.Line(0); got != "hi" {
		t.Fatalf("Line(0) = %q, want 'hi'", got)
	}
	if got := g.Line(1); got != "" {
		t.Fatalf("Line(1) = %q, want empty", got)
	}
}

func TestGridLineTrimsTrailingBlanks(t *testing.T) {
	g := terminal.Grid{
		Cols: 10, Rows: 1,
		Cells: []terminal.Cell{
			{Rune: 'a'}, {Rune: 'b'}, {Rune: ' '}, {Rune: 'c'},
			{Rune: ' '}, {Rune: ' '}, {Rune: ' '}, {Rune: ' '}, {Rune: ' '}, {Rune: ' '},
		},
	}
	if got := g.Line(0); got != "ab c" {
		t.Fatalf("Line(0) = %q, want 'ab c'", got)
	}
}

func TestGridLineNullRunesTreatedAsSpace(t *testing.T) {
	// Cells with zero rune should be treated as spaces and trimmed.
	g := terminal.Grid{
		Cols: 5, Rows: 1,
		Cells: []terminal.Cell{
			{Rune: 'x'}, {Rune: 0}, {Rune: 0}, {Rune: 0}, {Rune: 0},
		},
	}
	if got := g.Line(0); got != "x" {
		t.Fatalf("Line(0) = %q, want 'x'", got)
	}
}

func TestGridLineNegativeRowReturnsEmpty(t *testing.T) {
	g := makeTestGrid(5, 3, 'X')
	if got := g.Line(-1); got != "" {
		t.Fatalf("Line(-1) = %q, want empty", got)
	}
}

func TestGridLineRowTooLargeReturnsEmpty(t *testing.T) {
	g := makeTestGrid(5, 3, 'X')
	if got := g.Line(3); got != "" {
		t.Fatalf("Line(3) = %q, want empty", got)
	}
}

func TestGridLineAllBlankRow(t *testing.T) {
	g := terminal.Grid{
		Cols: 4, Rows: 1,
		Cells: []terminal.Cell{{Rune: ' '}, {Rune: ' '}, {Rune: ' '}, {Rune: ' '}},
	}
	if got := g.Line(0); got != "" {
		t.Fatalf("Line(0) all-blank = %q, want empty string", got)
	}
}

// ---------------------------------------------------------------------------
// Grid.String tests
// ---------------------------------------------------------------------------

func TestGridStringMultiRow(t *testing.T) {
	eng := terminal.NewTerminal(terminal.WithSize(10, 3))
	eng.Write([]byte("foo\r\nbar"))
	got := eng.Snapshot().String()
	if got != "foo\nbar" {
		t.Fatalf("String() = %q, want %q", got, "foo\nbar")
	}
}

func TestGridStringTrimsTrailingBlankRows(t *testing.T) {
	eng := terminal.NewTerminal(terminal.WithSize(10, 5))
	eng.Write([]byte("hi"))
	got := eng.Snapshot().String()
	if got != "hi" {
		t.Fatalf("String() = %q, want %q", got, "hi")
	}
}

func TestGridStringAllBlankReturnsEmpty(t *testing.T) {
	eng := terminal.NewTerminal(terminal.WithSize(10, 3))
	got := eng.Snapshot().String()
	if got != "" {
		t.Fatalf("empty grid String() = %q, want empty", got)
	}
}

func TestGridStringSingleRowNoTrailingNewline(t *testing.T) {
	eng := terminal.NewTerminal(terminal.WithSize(10, 2))
	eng.Write([]byte("hello"))
	got := eng.Snapshot().String()
	if got != "hello" {
		t.Fatalf("String() = %q, want 'hello' (no trailing newline)", got)
	}
}

// ---------------------------------------------------------------------------
// Cell and Attr tests
// ---------------------------------------------------------------------------

func TestBlankCell(t *testing.T) {
	if terminal.Blank.Rune != ' ' {
		t.Fatalf("Blank.Rune = %q, want ' '", terminal.Blank.Rune)
	}
	if terminal.Blank.FG != nil {
		t.Fatal("Blank.FG should be nil")
	}
	if terminal.Blank.BG != nil {
		t.Fatal("Blank.BG should be nil")
	}
	if terminal.Blank.Attr != 0 {
		t.Fatalf("Blank.Attr = %d, want 0", terminal.Blank.Attr)
	}
}

func TestAttrDistinctBits(t *testing.T) {
	// Verify each attribute occupies a distinct bit.
	attrs := []terminal.Attr{
		terminal.AttrBold,
		terminal.AttrItalic,
		terminal.AttrUnderline,
		terminal.AttrReverse,
	}
	for i, a := range attrs {
		for j, b := range attrs {
			if i != j && a&b != 0 {
				t.Fatalf("attrs[%d] and attrs[%d] share bits", i, j)
			}
		}
	}
}

func TestAttrCombine(t *testing.T) {
	combined := terminal.AttrBold | terminal.AttrItalic
	if combined&terminal.AttrBold == 0 {
		t.Fatal("combined should have AttrBold")
	}
	if combined&terminal.AttrItalic == 0 {
		t.Fatal("combined should have AttrItalic")
	}
	if combined&terminal.AttrUnderline != 0 {
		t.Fatal("combined should NOT have AttrUnderline")
	}
}

func TestAttrClear(t *testing.T) {
	a := terminal.AttrBold | terminal.AttrItalic | terminal.AttrUnderline
	a &^= terminal.AttrItalic
	if a&terminal.AttrBold == 0 || a&terminal.AttrUnderline == 0 {
		t.Fatal("Bold and Underline should remain after clearing Italic")
	}
	if a&terminal.AttrItalic != 0 {
		t.Fatal("Italic should have been cleared")
	}
}

func TestCellWithColor(t *testing.T) {
	fg := color.RGBA{R: 255, A: 255}
	bg := color.RGBA{G: 128, A: 255}
	c := terminal.Cell{
		Rune: 'Z',
		FG:   fg,
		BG:   bg,
		Attr: terminal.AttrBold | terminal.AttrUnderline,
	}
	if c.Rune != 'Z' {
		t.Fatalf("Rune = %q", c.Rune)
	}
	if c.FG != fg {
		t.Fatal("FG mismatch")
	}
	if c.BG != bg {
		t.Fatal("BG mismatch")
	}
	if c.Attr&terminal.AttrBold == 0 || c.Attr&terminal.AttrUnderline == 0 {
		t.Fatal("expected Bold+Underline attrs")
	}
}

// ---------------------------------------------------------------------------
// Engine: NewTerminal / WithSize
// ---------------------------------------------------------------------------

func TestNewTerminalDefaultSize(t *testing.T) {
	eng := terminal.NewTerminal()
	cols, rows := eng.Size()
	if cols != 80 || rows != 24 {
		t.Fatalf("default size = %dx%d, want 80x24", cols, rows)
	}
}

func TestNewTerminalCustomSize(t *testing.T) {
	eng := terminal.NewTerminal(terminal.WithSize(132, 43))
	cols, rows := eng.Size()
	if cols != 132 || rows != 43 {
		t.Fatalf("size = %dx%d, want 132x43", cols, rows)
	}
}

func TestNewTerminalInitialCursorVisible(t *testing.T) {
	eng := terminal.NewTerminal(terminal.WithSize(10, 5))
	_, _, vis := eng.Cursor()
	if !vis {
		t.Fatal("cursor should be visible on fresh terminal")
	}
}

func TestNewTerminalInitialCursorAtOrigin(t *testing.T) {
	eng := terminal.NewTerminal(terminal.WithSize(10, 5))
	col, row, _ := eng.Cursor()
	if col != 0 || row != 0 {
		t.Fatalf("initial cursor = (%d,%d), want (0,0)", col, row)
	}
}

// TestSnapshotIndependence verifies that modifying the engine after Snapshot
// does not mutate the already-returned grid.
func TestSnapshotIndependence(t *testing.T) {
	eng := terminal.NewTerminal(terminal.WithSize(10, 5))
	eng.Write([]byte("hello"))
	snap := eng.Snapshot()

	// Write more into the engine.
	eng.Write([]byte("\r\nnew"))
	if snap.Line(1) != "" {
		t.Fatalf("snapshot was mutated: line 1 = %q", snap.Line(1))
	}
}

// TestResizeToSmallerClampsContent ensures Resize does not panic when the new
// size is smaller than existing content.
func TestResizeToSmallerClampsContent(t *testing.T) {
	eng := terminal.NewTerminal(terminal.WithSize(10, 5))
	eng.Write([]byte("hello world"))
	eng.Resize(5, 3)
	cols, rows := eng.Size()
	if cols != 5 || rows != 3 {
		t.Fatalf("after resize: size = %dx%d, want 5x3", cols, rows)
	}
	// Should not panic and Line should still return a string.
	_ = eng.Snapshot().Line(0)
}

// TestResizeZeroIgnored verifies that a zero-dimension resize is a no-op.
func TestResizeZeroIgnored(t *testing.T) {
	eng := terminal.NewTerminal(terminal.WithSize(10, 5))
	eng.Resize(0, 5)
	eng.Resize(10, 0)
	eng.Resize(0, 0)
	cols, rows := eng.Size()
	if cols != 10 || rows != 5 {
		t.Fatalf("zero resize changed size to %dx%d", cols, rows)
	}
}

// TestDefaultEngine ensures DefaultEngine returns a functional Engine.
func TestDefaultEngine(t *testing.T) {
	eng := terminal.DefaultEngine(40, 10)
	if eng == nil {
		t.Fatal("DefaultEngine returned nil")
	}
	cols, rows := eng.Size()
	if cols != 40 || rows != 10 {
		t.Fatalf("DefaultEngine size = %dx%d, want 40x10", cols, rows)
	}
}