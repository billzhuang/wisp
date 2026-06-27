package terminal

import (
	"strings"
	"testing"
)

// BenchmarkEngineWrite measures raw VT parse + grid-update throughput of the
// pure-Go engine on a realistic mix of printable text, newlines, and SGR colour
// sequences. Report MB/s with: go test -run=^$ -bench=EngineWrite ./internal/terminal/
func BenchmarkEngineWrite(b *testing.B) {
	// ~1 KiB of "shell-like" output: coloured words, newlines, plain text.
	var sb strings.Builder
	for i := 0; i < 16; i++ {
		sb.WriteString("\x1b[32muser@host\x1b[0m:\x1b[34m~/src/wisp\x1b[0m$ ls -la\r\n")
		sb.WriteString("drwxr-xr-x  4 user staff  128 Jan  1 00:00 internal\r\n")
	}
	chunk := []byte(sb.String())

	e := NewTerminal(WithSize(120, 40))
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Write(chunk); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkEngineScroll isolates scroll throughput: once the grid is full every
// newline forces a scroll, so this measures the per-line scroll cost that bulk
// output (e.g. cat-ing a large file) is bound by.
func BenchmarkEngineScroll(b *testing.B) {
	chunk := []byte(strings.Repeat("x\r\n", 256))
	e := NewTerminal(WithSize(120, 40))
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Write(chunk); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSnapshot measures the cost of copying the grid for a render frame.
func BenchmarkSnapshot(b *testing.B) {
	e := NewTerminal(WithSize(120, 40))
	writeStr(e, strings.Repeat("x", 120*40))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = e.Snapshot()
	}
}
