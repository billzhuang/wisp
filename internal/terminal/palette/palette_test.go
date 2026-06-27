package palette

import (
	"image/color"
	"testing"
)

func rgba(c color.Color) (r, g, b, a uint8) {
	rr, gg, bb, aa := c.RGBA()
	return uint8(rr >> 8), uint8(gg >> 8), uint8(bb >> 8), uint8(aa >> 8)
}

func TestANSI16Clamp(t *testing.T) {
	if ANSI16(-5) != ANSI16(0) {
		t.Fatal("negative index should clamp to 0")
	}
	if ANSI16(99) != ANSI16(15) {
		t.Fatal("large index should clamp to 15")
	}
}

func TestIndex256Cube(t *testing.T) {
	// Index 16 is the corner of the cube => black.
	r, g, b, _ := rgba(Index256(16))
	if r != 0 || g != 0 || b != 0 {
		t.Fatalf("index 16 = %d,%d,%d, want 0,0,0", r, g, b)
	}
	// Index 231 is the opposite corner => white-ish (255,255,255).
	r, g, b, _ = rgba(Index256(231))
	if r != 255 || g != 255 || b != 255 {
		t.Fatalf("index 231 = %d,%d,%d, want 255,255,255", r, g, b)
	}
}

func TestIndex256Grayscale(t *testing.T) {
	r, g, b, _ := rgba(Index256(232))
	if r != g || g != b {
		t.Fatalf("grayscale not gray: %d,%d,%d", r, g, b)
	}
	if r != 8 {
		t.Fatalf("index 232 = %d, want 8", r)
	}
}

func TestExtended256(t *testing.T) {
	c, n := Extended([]int{38, 5, 21})
	if n != 3 {
		t.Fatalf("consumed = %d, want 3", n)
	}
	if c == nil {
		t.Fatal("nil color")
	}
}

func TestExtendedTruecolor(t *testing.T) {
	c, n := Extended([]int{48, 2, 1, 2, 3})
	if n != 5 {
		t.Fatalf("consumed = %d, want 5", n)
	}
	r, g, b, _ := rgba(c)
	if r != 1 || g != 2 || b != 3 {
		t.Fatalf("got %d,%d,%d, want 1,2,3", r, g, b)
	}
}

func TestExtendedMalformed(t *testing.T) {
	if _, n := Extended([]int{38}); n != 0 {
		t.Fatal("expected 0 consumed for truncated sequence")
	}
	if _, n := Extended([]int{38, 5}); n != 0 {
		t.Fatal("expected 0 consumed for missing index")
	}
	if _, n := Extended([]int{38, 9}); n != 0 {
		t.Fatal("expected 0 consumed for unknown selector")
	}
}

func TestExtendedClampsBytes(t *testing.T) {
	c, _ := Extended([]int{38, 2, 999, -5, 128})
	r, g, b, _ := rgba(c)
	if r != 255 || g != 0 || b != 128 {
		t.Fatalf("clamp got %d,%d,%d, want 255,0,128", r, g, b)
	}
}
