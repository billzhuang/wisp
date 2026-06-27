# wisp

A Tailscale-native terminal emulator. wisp embeds a **userspace Tailscale node
(tsnet)** directly in the binary, so it can SSH to hosts on a tailnet **with no
Tailscale app, daemon, or system client installed on the client machine**. The
terminal *is* the tailnet node.

The render path is built around the [Ghostty](https://ghostty.org) terminal
engine (libghostty) + [Ebitengine](https://ebitengine.org), with a pure-Go VT
engine and a stdio frontend as the always-buildable default.

> Status: the network half (tsnet + SSH + PTY) and the pure-Go terminal engine
> are complete and tested end-to-end. The libghostty cgo engine and the
> Ebitengine GPU frontend are real, reviewable code behind build tags; see
> [docs/BUILD.md](docs/BUILD.md) for their toolchain requirements.

## Why

Existing libghostty terminals that touch Tailscale (Shio, Chuchu) rely on the
**system** Tailscale client. wisp removes that dependency: connectivity comes
from `tailscale.com/tsnet` linked into the binary, with its own state directory
and auth key. The destination host must still be on the tailnet (or behind a
subnet router) — tsnet removes the *client-side* app, not the requirement that
the other end is reachable.

## Architecture

One Go process owns everything. The seam:

```text
[frontend: stdio (default) | Ebitengine (-tags ebiten)]
   ↑ draws grid             ↓ key/mouse events
[terminal.Engine] ← VT bytes ← stdout ─┐
   pure-go VT (default)                 │
   libghostty (-tags libghostty)        │
                              [sshx: x/crypto/ssh PTY + Shell]
                                         │ over net.Conn
                              [transport.Dialer]
                                tsnet.Server.Dial (default)
                                net.Dialer (-direct / tests)
                                         │ userspace WireGuard (gVisor netstack)
                                         ▼
                                     tailnet → host:22
```

Every layer is an interface so it can be swapped and tested in isolation:

| Layer | Package | Interface | Default impl | Alternate |
|---|---|---|---|---|
| Tailnet transport | `internal/transport` | `Dialer` | `TSNetDialer` (tsnet) | `NetDialer` (`-direct`, tests) |
| SSH session | `internal/sshx` | — | `x/crypto/ssh` over the dialer conn | |
| Terminal engine | `internal/terminal` | `Engine` | pure-Go VT parser | libghostty (`-tags libghostty`) |
| Frontend | `internal/render` | `Frontend` | stdio passthrough | Ebitengine (`-tags ebiten`), `Headless` (tests) |
| Controller | `internal/app` | — | wires SSH ↔ frontend | |
| Config | `internal/config` | — | flags + env | |

## Usage

```sh
# Interactive login URL on first run; node identity persists in -state-dir.
wisp -host dev-box -user alice

# Pre-authenticated / ephemeral node against a self-hosted control plane:
TS_AUTHKEY=tskey-... wisp -host dev-box -user alice \
  -control-url https://headscale.example.com -ephemeral

# Bypass tsnet entirely for a directly reachable host (no Tailscale):
wisp -direct -host 10.0.0.5 -user alice
```

Run `wisp -h` for the full flag list. Host keys are verified against
`~/.config/wisp/known_hosts` (trust-on-first-use; a *changed* key is always
rejected as a possible MITM).

## Auto-update

wisp updates itself from GitHub Releases, Ghostty-style — no package manager
required:

```sh
wisp -version          # print the running build
wisp -update           # check, download, verify, and install a newer release
wisp -no-update-check  # skip the startup "update available" notice
```

On startup a released build does a quick (≤2s, best-effort) check and prints a
one-line notice if a newer version exists. In the GUI frontend the notice
appears as a top banner; **press Ctrl+U to install** without leaving the
session. The updater downloads the asset for the running OS/arch, verifies its
SHA-256 against the release's checksums file, and atomically replaces the
binary; you restart wisp to run the new version.

This is driven by [`.github/workflows/release.yml`](.github/workflows/release.yml):
pushing a `vX.Y.Z` tag builds the binaries and a `checksums.txt` and publishes
them as a Release. The version is stamped into the binary at link time, and
`internal/update` reads exactly those asset names — so a tagged release becomes
a one-click upgrade for every user.

### Release target & flavors

The product targets **Apple Silicon macOS** (M-series). The GUI build uses
Ebitengine, which needs cgo + the macOS frameworks and therefore builds on a
native Apple-Silicon runner (cgo GUI binaries can't be cross-compiled — that's
why the headless CLI build, being pure Go, cross-compiles from any runner while
the GUI does not). Two flavors are published, and **each self-updates to its own
asset** (see `cmd/wisp/flavor_*.go`):

| Asset | Flavor | Build |
|---|---|---|
| `wisp-gui_darwin_arm64` | GPU terminal app (primary) | `-tags ebiten`, cgo |
| `wisp_darwin_arm64` | headless stdio/CLI | pure Go |

CI additionally builds + tests the GUI on Linux (under `xvfb`) as a fast,
display-independent smoke net, and compile-checks the GUI on macOS Apple Silicon
(the release target).

## Performance

Honest answer: the **default pure-Go VT engine is fast enough for interactive
use, but it is not Ghostty-class** on its own. A full 120×40 screen is ~5 KB and
parses in well under a millisecond, so typing, colour, and normal program output
feel instant. Bulk throughput (e.g. `cat`-ing a huge file) is bound by VT
parsing + grid scrolling — `go test -bench=EngineWrite ./internal/terminal/`
reports the current number on your machine.

Ghostty's "blazing fast" comes from a GPU glyph-cache renderer and a
SIMD-optimised VT parser written in Zig. wisp reaches that tier through its
pluggable backends, not by reimplementing them in pure Go:

- **`-tags libghostty`** swaps in the real Ghostty VT engine (libghostty-vt) —
  the same parser Ghostty ships — behind the identical `terminal.Engine`
  interface (see [docs/BUILD.md](docs/BUILD.md)).
- **`-tags ebiten`** renders the grid on the GPU rather than the CPU.

So the architecture is built for Ghostty-class speed; the pure-Go path is the
always-portable, zero-toolchain fallback. (A row-ring scroll buffer is the next
easy win for the pure-Go engine and is tracked as a follow-up.)

## Building & testing

```sh
go build ./...            # default build: tsnet + pure-go VT + stdio frontend
go test ./...             # unit + integration tests (no tailnet/GPU needed)
go test -race ./...
```

The default build needs only the Go toolchain. The `-tags ebiten` and
`-tags libghostty` builds need extra toolchains — see
[docs/BUILD.md](docs/BUILD.md).

## Tests

- **Unit:** VT parser (text, control codes, CSI, erase, SGR/256/truecolour,
  UTF-8 across Write boundaries, scrolling, resize, concurrency), colour
  palette, config parsing/validation, transport, host-key TOFU, the headless
  frontend.
- **Integration:** `internal/app/integration_test.go` and
  `internal/sshx/sshx_test.go` stand up a real in-process SSH server
  (`internal/testutil/sshserver`), dial it through the `Dialer` seam, allocate a
  PTY, run a shell/command, pipe the output through the terminal engine, and
  assert the rendered grid — exercising the whole network → SSH → PTY → engine
  pipeline without a tailnet or a display.

## Layout

```text
cmd/wisp/                main: flag parsing, auth, dialer selection, wiring
internal/transport/      Dialer: tsnet node (no daemon) + plain net
internal/sshx/           SSH client over the dialer conn; known_hosts TOFU
internal/terminal/       Engine + pure-Go VT parser; libghostty scaffold
internal/terminal/palette ANSI/256/truecolour mapping
internal/render/         Frontend: stdio + Ebitengine + headless
internal/app/            Controller wiring SSH ↔ frontend
internal/config/         flags + env → validated Config
internal/update/         GitHub Releases self-update (check, verify, replace)
internal/version/        build version (ldflags-injected by CD)
internal/testutil/sshserver in-process SSH server for tests
.github/workflows/       ci (build/test/vet) + release (tag → binaries + checksums)
docs/BUILD.md            libghostty (Zig) + Ebitengine toolchain notes
```
