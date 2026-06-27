//go:build !libghostty

package terminal

// DefaultEngine constructs the terminal engine used when the binary is built
// without the `libghostty` tag: the pure-Go VT parser. This is the build that
// needs no cgo, no Zig, and no libghostty-vt, so it always works and is what the
// tests exercise.
func DefaultEngine(cols, rows int) Engine {
	return NewTerminal(WithSize(cols, rows))
}

// Backend names the active engine implementation, for --version / logging.
const Backend = "pure-go-vt"
