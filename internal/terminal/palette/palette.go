// Package palette maps ANSI/SGR colour codes to concrete RGBA values. It is
// kept separate from the parser so colour handling can be unit-tested in
// isolation and reused by any renderer.
package palette

import "image/color"

// ansi16 is the standard 16-colour palette (xterm defaults).
var ansi16 = [16]color.RGBA{
	{0x00, 0x00, 0x00, 0xff}, // 0 black
	{0xcd, 0x00, 0x00, 0xff}, // 1 red
	{0x00, 0xcd, 0x00, 0xff}, // 2 green
	{0xcd, 0xcd, 0x00, 0xff}, // 3 yellow
	{0x00, 0x00, 0xee, 0xff}, // 4 blue
	{0xcd, 0x00, 0xcd, 0xff}, // 5 magenta
	{0x00, 0xcd, 0xcd, 0xff}, // 6 cyan
	{0xe5, 0xe5, 0xe5, 0xff}, // 7 white
	{0x7f, 0x7f, 0x7f, 0xff}, // 8 bright black
	{0xff, 0x00, 0x00, 0xff}, // 9 bright red
	{0x00, 0xff, 0x00, 0xff}, // 10 bright green
	{0xff, 0xff, 0x00, 0xff}, // 11 bright yellow
	{0x5c, 0x5c, 0xff, 0xff}, // 12 bright blue
	{0xff, 0x00, 0xff, 0xff}, // 13 bright magenta
	{0x00, 0xff, 0xff, 0xff}, // 14 bright cyan
	{0xff, 0xff, 0xff, 0xff}, // 15 bright white
}

// colors256 is the full xterm 256-colour palette pre-boxed into color.Color
// interface values. Boxing a color.RGBA value into an interface heap-allocates,
// so the parser's hot path (an SGR colour change per coloured run of output)
// would allocate on every call if ANSI16/Index256 boxed a fresh value each time.
// Doing it once at init makes the lookups allocation-free.
var colors256 [256]color.Color

func init() {
	for i := range colors256 {
		colors256[i] = rgba256(i)
	}
}

// rgba256 is the raw xterm colour for 256-palette index i (0-255):
//   - 0-15  : the standard 16 colours
//   - 16-231: a 6x6x6 RGB cube
//   - 232-255: a 24-step grayscale ramp
func rgba256(i int) color.RGBA {
	switch {
	case i < 16:
		return ansi16[i]
	case i < 232:
		i -= 16
		r := i / 36
		g := (i / 6) % 6
		b := i % 6
		return color.RGBA{cubeStep(r), cubeStep(g), cubeStep(b), 0xff}
	default: // 232-255
		v := uint8(8 + (i-232)*10)
		return color.RGBA{v, v, v, 0xff}
	}
}

// ANSI16 returns one of the 16 standard colours. Out-of-range indices clamp.
func ANSI16(i int) color.Color {
	if i < 0 {
		i = 0
	}
	if i > 15 {
		i = 15
	}
	return colors256[i]
}

// Index256 returns the colour for an xterm 256-colour index (see rgba256).
// Negative indices clamp to 0; indices above 255 clamp to bright white.
func Index256(i int) color.Color {
	switch {
	case i < 0:
		return colors256[0]
	case i < 256:
		return colors256[i]
	default:
		return colors256[15]
	}
}

func cubeStep(v int) uint8 {
	if v == 0 {
		return 0
	}
	return uint8(55 + v*40)
}

// Extended parses an SGR extended-colour sub-sequence beginning at params[0],
// which is 38 or 48. It supports:
//
//	38;5;n        -> 256-colour index n
//	38;2;r;g;b    -> 24-bit truecolour
//
// It returns the resolved colour and the number of params consumed (including
// the leading 38/48). A consumed count of 0 signals a malformed sequence.
func Extended(params []int) (color.Color, int) {
	if len(params) < 2 {
		return nil, 0
	}
	switch params[1] {
	case 5:
		if len(params) < 3 {
			return nil, 0
		}
		return Index256(params[2]), 3
	case 2:
		if len(params) < 5 {
			return nil, 0
		}
		return color.RGBA{
			uint8(clampByte(params[2])),
			uint8(clampByte(params[3])),
			uint8(clampByte(params[4])),
			0xff,
		}, 5
	default:
		return nil, 0
	}
}

func clampByte(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}
