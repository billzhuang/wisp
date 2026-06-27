package terminal

import (
	"testing"
	"unicode/utf8"
)

// TestUTF8AccASCII verifies that ASCII bytes are returned immediately without
// buffering.
func TestUTF8AccASCII(t *testing.T) {
	var u utf8acc
	for _, b := range []byte("hello") {
		r, ok := u.feed(b)
		if !ok {
			t.Fatalf("feed(%q): expected immediate rune, got pending", b)
		}
		if r != rune(b) {
			t.Fatalf("feed(%q): got %q, want %q", b, r, rune(b))
		}
		if u.pending() {
			t.Fatal("pending() should be false after ASCII rune")
		}
	}
}

// TestUTF8AccTwoByteSequence checks a 2-byte UTF-8 rune (é = 0xc3 0xa9).
func TestUTF8AccTwoByteSequence(t *testing.T) {
	var u utf8acc

	// First byte: leading byte of 2-byte sequence.
	r, ok := u.feed(0xc3)
	if ok {
		t.Fatal("expected pending after first byte of 2-byte sequence")
	}
	if r != 0 {
		t.Fatalf("first byte returned non-zero rune %q", r)
	}
	if !u.pending() {
		t.Fatal("pending() should be true after first byte")
	}

	// Second byte: continuation.
	r, ok = u.feed(0xa9)
	if !ok {
		t.Fatal("expected complete rune after second byte")
	}
	if r != 'é' {
		t.Fatalf("got %q (%d), want é", r, r)
	}
	if u.pending() {
		t.Fatal("pending() should be false after complete sequence")
	}
}

// TestUTF8AccThreeByteSequence checks a 3-byte UTF-8 rune (€ = U+20AC = 0xe2
// 0x82 0xac).
func TestUTF8AccThreeByteSequence(t *testing.T) {
	var u utf8acc

	r, ok := u.feed(0xe2)
	if ok || u.pending() == false {
		t.Fatal("expected pending after byte 1")
	}
	if r != 0 {
		t.Fatalf("unexpected rune %q after byte 1", r)
	}

	r, ok = u.feed(0x82)
	if ok || u.pending() == false {
		t.Fatal("expected pending after byte 2")
	}
	if r != 0 {
		t.Fatalf("unexpected rune %q after byte 2", r)
	}

	r, ok = u.feed(0xac)
	if !ok {
		t.Fatal("expected complete rune after byte 3")
	}
	if r != '€' {
		t.Fatalf("got %q, want €", r)
	}
	if u.pending() {
		t.Fatal("pending() should be false after complete sequence")
	}
}

// TestUTF8AccFourByteSequence checks a 4-byte UTF-8 rune (😀 = U+1F600 =
// 0xf0 0x9f 0x98 0x80).
func TestUTF8AccFourByteSequence(t *testing.T) {
	var u utf8acc
	emoji := '😀'
	b := make([]byte, 4)
	utf8.EncodeRune(b, emoji)

	for i, byt := range b {
		r, ok := u.feed(byt)
		if i < 3 {
			if ok {
				t.Fatalf("byte %d: unexpected complete rune %q", i, r)
			}
			if !u.pending() {
				t.Fatalf("byte %d: expected pending", i)
			}
		} else {
			if !ok {
				t.Fatal("expected complete rune after final byte")
			}
			if r != emoji {
				t.Fatalf("got %q, want %q", r, emoji)
			}
		}
	}
	if u.pending() {
		t.Fatal("pending() should be false after complete sequence")
	}
}

// TestUTF8AccInvalidLeadingByte checks that an invalid leading byte immediately
// emits RuneError.
func TestUTF8AccInvalidLeadingByte(t *testing.T) {
	var u utf8acc

	// 0xfe and 0xff are invalid UTF-8 leading bytes.
	for _, b := range []byte{0xfe, 0xff} {
		r, ok := u.feed(b)
		if !ok {
			t.Fatalf("feed(0x%02x): expected immediate result, got pending", b)
		}
		if r != utf8.RuneError {
			t.Fatalf("feed(0x%02x): got %q, want RuneError", b, r)
		}
		if u.pending() {
			t.Fatal("should not be pending after invalid leading byte")
		}
	}
}

// TestUTF8AccInvalidContinuationByte checks that a bad continuation byte emits
// RuneError and that the accumulator resets cleanly.
func TestUTF8AccInvalidContinuationByte(t *testing.T) {
	var u utf8acc

	// Start a 2-byte sequence.
	r, ok := u.feed(0xc3)
	if ok {
		t.Fatal("expected pending")
	}
	_ = r

	// Feed a non-continuation byte (must be 10xxxxxx, but 'X' is 0x58).
	r, ok = u.feed('X')
	if !ok {
		t.Fatal("expected immediate RuneError on bad continuation")
	}
	if r != utf8.RuneError {
		t.Fatalf("got %q, want RuneError", r)
	}

	// The accumulator must have reset: next ASCII byte should work normally.
	r, ok = u.feed('A')
	if !ok || r != 'A' {
		t.Fatalf("after reset: feed('A') = %q, %v", r, ok)
	}
	if u.pending() {
		t.Fatal("should not be pending after reset + ASCII")
	}
}

// TestUTF8AccPendingState verifies pending() tracks in-progress sequences.
func TestUTF8AccPendingState(t *testing.T) {
	var u utf8acc
	if u.pending() {
		t.Fatal("fresh accumulator should not be pending")
	}

	// Start a 3-byte sequence.
	u.feed(0xe2)
	if !u.pending() {
		t.Fatal("should be pending after first byte of 3-byte sequence")
	}
	u.feed(0x82)
	if !u.pending() {
		t.Fatal("should be pending after second byte of 3-byte sequence")
	}
	u.feed(0xac) // complete
	if u.pending() {
		t.Fatal("should not be pending after complete sequence")
	}
}

// TestUTF8AccSplitAcrossMultipleCalls simulates the terminal scenario where a
// multi-byte rune is split across Write calls (one byte at a time).
func TestUTF8AccSplitAcrossMultipleCalls(t *testing.T) {
	var u utf8acc

	// Encode "中" (U+4E2D = 0xe4 0xb8 0xad) and feed one byte per "call".
	target := '中'
	b := make([]byte, 3)
	utf8.EncodeRune(b, target)

	var result rune
	for i, byt := range b {
		r, ok := u.feed(byt)
		if i < 2 {
			if ok {
				t.Fatalf("byte %d: unexpected complete rune", i)
			}
		} else {
			if !ok {
				t.Fatal("expected complete rune after last byte")
			}
			result = r
		}
	}
	if result != target {
		t.Fatalf("got %q, want %q", result, target)
	}
}

// TestUTF8AccMultipleSequences verifies the accumulator correctly handles
// several complete sequences in a row.
func TestUTF8AccMultipleSequences(t *testing.T) {
	var u utf8acc
	// "héllo" — 'h' ASCII, 'é' 2-byte, then ASCII
	input := []byte("héllo")
	expected := []rune("héllo")

	out := make([]rune, 0, len(expected))
	for _, b := range input {
		r, ok := u.feed(b)
		if ok {
			out = append(out, r)
		}
	}
	if string(out) != string(expected) {
		t.Fatalf("got %q, want %q", string(out), string(expected))
	}
}