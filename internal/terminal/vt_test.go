package terminal

import (
	"image/color"
	"strings"
	"testing"

	"github.com/billzhuang/wisp/internal/terminal/palette"
)

func writeStr(e Engine, s string) {
	if _, err := e.Write([]byte(s)); err != nil {
		panic(err)
	}
}

func TestPlainText(t *testing.T) {
	e := NewTerminal(WithSize(20, 5))
	writeStr(e, "hello")
	g := e.Snapshot()
	if got := g.Line(0); got != "hello" {
		t.Fatalf("line 0 = %q, want %q", got, "hello")
	}
	col, row, vis := e.Cursor()
	if col != 5 || row != 0 || !vis {
		t.Fatalf("cursor = (%d,%d,%v), want (5,0,true)", col, row, vis)
	}
}

func TestCRLF(t *testing.T) {
	e := NewTerminal(WithSize(20, 5))
	writeStr(e, "line1\r\nline2")
	g := e.Snapshot()
	if g.Line(0) != "line1" || g.Line(1) != "line2" {
		t.Fatalf("got lines %q / %q", g.Line(0), g.Line(1))
	}
}

func TestBackspaceAndOverwrite(t *testing.T) {
	e := NewTerminal(WithSize(20, 3))
	writeStr(e, "abc\b\bX")
	if got := e.Snapshot().Line(0); got != "aXc" {
		t.Fatalf("line = %q, want %q", got, "aXc")
	}
}

func TestTab(t *testing.T) {
	e := NewTerminal(WithSize(20, 3))
	writeStr(e, "a\tb")
	g := e.Snapshot()
	if g.At(0, 0).Rune != 'a' {
		t.Fatalf("col0 = %q", g.At(0, 0).Rune)
	}
	if g.At(8, 0).Rune != 'b' {
		t.Fatalf("expected 'b' at col 8, got %q at %q", g.At(8, 0).Rune, g.Line(0))
	}
}

func TestLineWrap(t *testing.T) {
	e := NewTerminal(WithSize(4, 3))
	writeStr(e, "abcdef")
	g := e.Snapshot()
	if g.Line(0) != "abcd" || g.Line(1) != "ef" {
		t.Fatalf("wrap got %q / %q", g.Line(0), g.Line(1))
	}
}

func TestScrollUp(t *testing.T) {
	e := NewTerminal(WithSize(5, 2))
	writeStr(e, "r1\r\nr2\r\nr3")
	g := e.Snapshot()
	// First line should have scrolled off; r2, r3 remain.
	if g.Line(0) != "r2" || g.Line(1) != "r3" {
		t.Fatalf("scroll got %q / %q", g.Line(0), g.Line(1))
	}
}

// TestScrollUpRecyclesRowsBlank scrolls more times than the grid is tall, with
// shrinking line widths, so rows that once held wide text are recycled for
// narrower text. A ring buffer that under-clears a recycled row would leak the
// old wide content; every cell past the live text must read as a blank space.
func TestScrollUpRecyclesRowsBlank(t *testing.T) {
	e := NewTerminal(WithSize(5, 2))
	writeStr(e, "AAAAA\r\nBBBBB\r\nccc\r\nd")
	g := e.Snapshot()
	if g.Line(0) != "ccc" || g.Line(1) != "d" {
		t.Fatalf("got %q / %q, want %q / %q", g.Line(0), g.Line(1), "ccc", "d")
	}
	for c := 1; c < 5; c++ { // cols after "d" must be blank, not stale 'B'/'A'
		if r := g.At(c, 1).Rune; r != ' ' {
			t.Fatalf("stale cell at col %d of recycled row: %q", c, r)
		}
	}
}

// TestReverseIndexScrollsDown exercises scrollDown via RI (ESC M) at the top of
// the screen: content moves down a row and the bottom row is recycled as a
// cleared top row.
func TestReverseIndexScrollsDown(t *testing.T) {
	e := NewTerminal(WithSize(5, 3))
	writeStr(e, "r1\r\nr2\r\nr3") // rows: r1, r2, r3 (cursor on last row)
	writeStr(e, "\x1b[H")         // cursor home (top-left)
	writeStr(e, "\x1bM")          // RI at top → scroll down
	g := e.Snapshot()
	if g.Line(0) != "" || g.Line(1) != "r1" || g.Line(2) != "r2" {
		t.Fatalf("after RI got %q / %q / %q, want \"\" / r1 / r2",
			g.Line(0), g.Line(1), g.Line(2))
	}
}

// TestReverseIndexRecyclesRowsBlank is the scrollDown mirror of
// TestScrollUpRecyclesRowsBlank: a wide bottom row is recycled to the top, so
// an under-clearing ring would leak its tail past the new narrow content.
func TestReverseIndexRecyclesRowsBlank(t *testing.T) {
	e := NewTerminal(WithSize(5, 2))
	writeStr(e, "r0\r\nWIDEX") // row0 "r0", row1 "WIDEX"
	writeStr(e, "\x1b[H")      // cursor home
	writeStr(e, "\x1bMz")      // RI scrolls down (recycles the wide row to top), then 'z'
	g := e.Snapshot()
	if g.Line(0) != "z" || g.Line(1) != "r0" {
		t.Fatalf("got %q / %q, want %q / %q", g.Line(0), g.Line(1), "z", "r0")
	}
	for c := 1; c < 5; c++ { // recycled top row must be blank, not stale "IDEX"
		if r := g.At(c, 0).Rune; r != ' ' {
			t.Fatalf("stale cell at col %d of recycled top row: %q", c, r)
		}
	}
}

func TestCursorPositionAndOverwrite(t *testing.T) {
	e := NewTerminal(WithSize(10, 4))
	writeStr(e, "\x1b[2;3HX") // CUP row2 col3
	g := e.Snapshot()
	if g.At(2, 1).Rune != 'X' {
		t.Fatalf("expected X at (2,1), grid:\n%s", g.String())
	}
}

func TestCursorMovementCSI(t *testing.T) {
	e := NewTerminal(WithSize(10, 4))
	writeStr(e, "A\x1b[2CB")  // A, cursor forward 2, B  => A at0, B at3
	writeStr(e, "\x1b[1;1HC") // home, C overwrites A
	g := e.Snapshot()
	if g.At(0, 0).Rune != 'C' {
		t.Fatalf("expected C at (0,0), got %q", g.At(0, 0).Rune)
	}
	if g.At(3, 0).Rune != 'B' {
		t.Fatalf("expected B at (3,0), got %q line=%q", g.At(3, 0).Rune, g.Line(0))
	}
}

func TestEraseLine(t *testing.T) {
	e := NewTerminal(WithSize(10, 2))
	writeStr(e, "hello\x1b[1;1H\x1b[K") // home then erase to EOL
	if got := e.Snapshot().Line(0); got != "" {
		t.Fatalf("erase line got %q, want empty", got)
	}
}

func TestEraseDisplay(t *testing.T) {
	e := NewTerminal(WithSize(10, 3))
	writeStr(e, "a\r\nb\r\nc\x1b[2J")
	g := e.Snapshot()
	if g.String() != "" {
		t.Fatalf("erase display got %q, want empty", g.String())
	}
}

func TestSGRColors(t *testing.T) {
	e := NewTerminal(WithSize(10, 2))
	writeStr(e, "\x1b[31;1mR\x1b[0mn")
	g := e.Snapshot()
	rcell := g.At(0, 0)
	if rcell.Rune != 'R' {
		t.Fatalf("expected R, got %q", rcell.Rune)
	}
	if rcell.Attr&AttrBold == 0 {
		t.Fatalf("expected bold attr on R")
	}
	if !colorsEqual(rcell.FG, palette.ANSI16(1)) {
		t.Fatalf("expected red fg, got %v", rcell.FG)
	}
	ncell := g.At(1, 0)
	if ncell.Attr != 0 || ncell.FG != nil {
		t.Fatalf("expected reset cell after SGR 0, got %+v", ncell)
	}
}

func TestSGRTruecolor(t *testing.T) {
	e := NewTerminal(WithSize(10, 2))
	writeStr(e, "\x1b[38;2;10;20;30mX")
	c := e.Snapshot().At(0, 0)
	want := color.RGBA{10, 20, 30, 0xff}
	if !colorsEqual(c.FG, want) {
		t.Fatalf("truecolor fg = %v, want %v", c.FG, want)
	}
}

func TestSGR256(t *testing.T) {
	e := NewTerminal(WithSize(10, 2))
	writeStr(e, "\x1b[48;5;196mX") // 256-color bg index 196
	c := e.Snapshot().At(0, 0)
	if !colorsEqual(c.BG, palette.Index256(196)) {
		t.Fatalf("256 bg = %v, want %v", c.BG, palette.Index256(196))
	}
}

func TestUTF8MultibyteAndSplit(t *testing.T) {
	e := NewTerminal(WithSize(10, 2))
	// "héllo wörld 😀" with the emoji written one byte at a time to exercise
	// the cross-Write accumulator.
	writeStr(e, "h\xc3\xa9llo")
	for _, b := range []byte(" 😀") {
		// Feed raw bytes one at a time (string(b) would UTF-8 re-encode the
		// byte and defeat the cross-Write accumulator this test exercises).
		if _, err := e.Write([]byte{b}); err != nil {
			t.Fatal(err)
		}
	}
	g := e.Snapshot()
	line := g.Line(0)
	if !strings.HasPrefix(line, "héllo") {
		t.Fatalf("utf8 line = %q", line)
	}
	if !strings.Contains(line, "😀") {
		t.Fatalf("expected emoji in %q", line)
	}
}

// TestBrokenUTF8ByteIsReprocessed ensures a byte that interrupts a multi-byte
// UTF-8 sequence (here ESC starting an SGR sequence) is not dropped: the color
// must still take effect on the following character.
func TestBrokenUTF8ByteIsReprocessed(t *testing.T) {
	e := NewTerminal(WithSize(10, 2))
	// 0xc3 starts a 2-byte sequence; ESC breaks it. The incomplete sequence
	// emits a replacement rune at (0,0), and the reprocessed "\x1b[31m" must
	// still be honoured so the following 'A' (at col 1) is red.
	writeStr(e, "\xc3\x1b[31mA")
	g := e.Snapshot()
	c := g.At(1, 0)
	if c.Rune != 'A' {
		t.Fatalf("expected 'A' at (1,0), got %q (line %q)", c.Rune, g.Line(0))
	}
	if c.FG == nil {
		t.Fatal("expected SGR colour to apply after broken UTF-8 byte was reprocessed")
	}
}

func TestResizePreservesContent(t *testing.T) {
	e := NewTerminal(WithSize(10, 4))
	writeStr(e, "keep")
	e.Resize(20, 6)
	cols, rows := e.Size()
	if cols != 20 || rows != 6 {
		t.Fatalf("size = %dx%d", cols, rows)
	}
	if got := e.Snapshot().Line(0); got != "keep" {
		t.Fatalf("after resize line = %q", got)
	}
}

func TestCursorVisibility(t *testing.T) {
	e := NewTerminal(WithSize(5, 2))
	writeStr(e, "\x1b[?25l")
	if _, _, vis := e.Cursor(); vis {
		t.Fatal("expected cursor hidden")
	}
	writeStr(e, "\x1b[?25h")
	if _, _, vis := e.Cursor(); !vis {
		t.Fatal("expected cursor visible")
	}
}

func TestConcurrentWriteAndSnapshot(t *testing.T) {
	e := NewTerminal(WithSize(40, 10))
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			writeStr(e, "x")
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_ = e.Snapshot()
		_, _, _ = e.Cursor()
	}
	<-done
}

func colorsEqual(a, b color.Color) bool {
	if a == nil || b == nil {
		return a == b
	}
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}
