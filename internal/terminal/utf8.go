package terminal

import "unicode/utf8"

// utf8acc accumulates UTF-8 byte sequences that may straddle Write boundaries.
// Terminal output arrives in arbitrary chunks, so a multi-byte rune can be split
// across two Write calls; the parser feeds bytes one at a time and this buffers
// continuation bytes until a full rune is available.
type utf8acc struct {
	buf  [4]byte
	n    int // bytes buffered
	need int // total bytes expected for the rune in progress (0 = none)
}

func (u *utf8acc) pending() bool { return u.need > 0 }

// feed adds one byte. It returns (rune, true) when a complete rune is decoded,
// otherwise (0, false) while more continuation bytes are required. Invalid
// sequences decode to utf8.RuneError so the stream never stalls.
func (u *utf8acc) feed(b byte) (rune, bool) {
	if u.need == 0 {
		switch {
		case b < 0x80:
			return rune(b), true
		case b&0xe0 == 0xc0:
			u.need = 2
		case b&0xf0 == 0xe0:
			u.need = 3
		case b&0xf8 == 0xf0:
			u.need = 4
		default:
			// Invalid leading byte.
			return utf8.RuneError, true
		}
		u.buf[0] = b
		u.n = 1
		return 0, false
	}

	// Continuation byte must match 10xxxxxx.
	if b&0xc0 != 0x80 {
		// Malformed: emit replacement and restart with this byte.
		u.reset()
		return utf8.RuneError, true
	}
	u.buf[u.n] = b
	u.n++
	if u.n < u.need {
		return 0, false
	}
	r, _ := utf8.DecodeRune(u.buf[:u.n])
	u.reset()
	return r, true
}

func (u *utf8acc) reset() {
	u.n = 0
	u.need = 0
}
