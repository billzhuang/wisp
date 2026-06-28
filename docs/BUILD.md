# Build guide

The **default build is pure Go** and needs only the Go toolchain (1.26+):

```sh
go build ./...
go test ./...
```

That build uses the embedded tsnet node, the pure-Go VT engine, and the stdio
frontend — the whole tailnet → proxy → local PTY → engine pipeline, no cgo, no GPU.

Two optional build tags swap in the heavier components. They are kept behind
tags precisely so the default build always works and CI can run the full test
suite without these toolchains.

---

## `-tags libghostty` — the libghostty VT engine

`go-libghostty` is cgo over `libghostty-vt`, which is built from the Ghostty
source tree with **Zig** (Zig doubles as the C compiler; there is no Zig code to
write). libghostty-vt only depends on libc, so static linking and
cross-compilation are straightforward.

```sh
# 1. Build libghostty-vt from the ghostty source tree:
zig build -Demit-lib-vt -Dtarget=x86_64-linux-gnu --prefix /tmp/ghostty-linux-amd64
#   macOS: -Dtarget=aarch64-macos  (or x86_64-macos)

# 2. cgo build of wisp against it:
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
  CC="zig cc -target x86_64-linux-gnu" \
  CXX="zig c++ -target x86_64-linux-gnu" \
  CGO_CFLAGS="-I/tmp/ghostty-linux-amd64/include -DGHOSTTY_STATIC" \
  CGO_LDFLAGS="-L/tmp/ghostty-linux-amd64/lib -lghostty-vt" \
  go build -tags libghostty ./...
# (or set PKG_CONFIG_PATH=/path/to/libghostty-vt/share/pkgconfig)
```

Notes:

- **CGO_ENABLED=1 is mandatory** for this tag. tsnet itself is pure Go, so it is
  fine inside a cgo build.
- **Pin commits.** go-libghostty and ghostty are both pre-1.0 with explicitly
  unstable APIs — pin a specific commit of each.
- **Render-state API (verify first).** `libghostty-vt` manages render state and
  Ghostling renders from it, but confirm whether **go-libghostty** currently
  exposes cell-level grid/style/cursor iteration or only parse + a
  plain/HTML formatter. If the grid is not exposed, either bind the additional
  libghostty-vt C render-state functions yourself (the same ones Ghostling
  uses) or use the formatter output as an interim crutch. This determines how
  much of `internal/terminal/libghostty.go` must be filled in.

`internal/terminal/libghostty.go` is the scaffold: it satisfies the same
`terminal.Engine` contract as the pure-Go engine and currently falls back to the
pure-Go engine so the tagged build links and runs. Swap in the cgo binding once
the render-state API is confirmed; nothing else in wisp changes.

---

## `-tags ebiten` — the Ebitengine GPU frontend

Ebitengine is pure Go but its windowing/GPU layer (GLFW) needs the platform
development headers at build time.

```sh
# Linux (Debian/Ubuntu):
sudo apt-get install -y \
  libgl1-mesa-dev libxrandr-dev libxcursor-dev libxinerama-dev libxi-dev libxxf86vm-dev

go build -tags ebiten ./...
```

macOS needs only the Xcode command-line tools (system frameworks). The frontend
in `internal/render/ebiten.go` draws the engine's cell grid with a bundled
monospace bitmap font (no external asset) and forwards keyboard input
(printable chars, Enter/Backspace/Tab/Esc, arrow keys in xterm normal mode).

Polish left as follow-ups (Phase 4): shaped fonts for wide-char / ligature
support, full Kitty keyboard protocol, mouse, and scrollback UI.

---

## Toolchain proof (Phase 0)

To validate the libghostty toolchain in isolation before wiring it in, build
libghostty-vt as above and run the go-libghostty hello-world (feed VT bytes into
a `Terminal`, format to plain text). The pure-Go engine in this repo mirrors
that contract, so `go test ./internal/terminal/...` is the equivalent check for
the default build.
