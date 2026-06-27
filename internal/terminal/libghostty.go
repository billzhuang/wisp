//go:build libghostty

// This file is the libghostty-backed terminal engine. It is compiled only with
// `-tags libghostty`, which additionally requires cgo and a libghostty-vt static
// library built from the Ghostty source tree with Zig (see docs/BUILD.md):
//
//	zig build -Demit-lib-vt -Dtarget=<triple> --prefix /tmp/ghostty
//	CGO_ENABLED=1 CC="zig cc -target <triple>" \
//	  CGO_CFLAGS="-I/tmp/ghostty/include -DGHOSTTY_STATIC" \
//	  CGO_LDFLAGS="-L/tmp/ghostty/lib -lghostty-vt" \
//	  go build -tags libghostty ./...
//
// The default build (engine_default.go) uses the pure-Go VT parser instead, so
// the project always builds and tests without this toolchain. When the Zig +
// libghostty-vt toolchain is available, fill in the cgo bindings below against
// go.mitchellh.com/libghostty (mirror github.com/mitchellh/go-libghostty),
// keeping the Engine contract identical so nothing else in wisp changes.
//
// NOTE: this is a scaffold. The cgo binding to go-libghostty is pinned per the
// build docs; the body is intentionally left to the toolchain-enabled build
// because go-libghostty's render-state (cell grid) API surface must be verified
// at integration time (see docs/BUILD.md "Render-state API" risk).

package terminal

import "fmt"

// DefaultEngine constructs the libghostty-backed engine under the libghostty
// build tag. Until the cgo binding is wired against a pinned go-libghostty +
// ghostty commit, it returns the pure-Go engine so the tagged build still links
// and runs; swap NewTerminal for the libghostty constructor once the binding is
// in place.
func DefaultEngine(cols, rows int) Engine {
	// TODO(libghostty): replace with newLibghosttyEngine(cols, rows) once the
	// go-libghostty cell-grid API is bound. See docs/BUILD.md.
	return NewTerminal(WithSize(cols, rows))
}

// Backend names the active engine implementation, for --version / logging.
const Backend = "libghostty (scaffold; using pure-go fallback until cgo binding wired)"

// ensure fmt stays imported for future binding diagnostics without churn.
var _ = fmt.Sprintf
