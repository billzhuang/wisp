# wisp

A Tailscale-native terminal emulator. wisp embeds a **userspace Tailscale node
(tsnet)** directly in the binary and runs a **local shell** whose network egress
is routed through the tailnet — **with no Tailscale app, daemon, or system
client installed, and without touching the host's DNS or routing**. The terminal
*is* the tailnet node, so tools you run in it (curl, git, Claude Code, Codex, …)
can reach tailnet and subnet-router resources directly.

The render path is built around the [Ghostty](https://ghostty.org) terminal
engine (libghostty) + [Ebitengine](https://ebitengine.org), with a pure-Go VT
engine and a stdio frontend as the always-buildable default.

> Status: the network half (tsnet + egress proxy + local PTY) and the pure-Go
> terminal engine are complete and tested end-to-end. The libghostty cgo engine
> and the Ebitengine GPU frontend are real, reviewable code behind build tags;
> see [docs/BUILD.md](docs/BUILD.md) for their toolchain requirements.

## Why

Installing the **system** Tailscale client adds a kernel TUN device and rewrites
the host's DNS and routing — which can conflict with a corporate VPN or local
DNS setup. wisp avoids all of that: connectivity comes from `tailscale.com/tsnet`
linked into the binary, a *userspace* network stack with its own state directory
and auth key. No TUN device, no DNS changes, no daemon.

The catch with a userspace stack is that it can't *transparently* capture other
programs' traffic the way a TUN device does. wisp bridges that gap with a small
**local proxy**: tsnet provides the connectivity, the proxy exposes it on
loopback, and wisp injects `HTTP(S)_PROXY` / `ALL_PROXY` into the shell's
environment. Virtually every modern tool honors those variables, and the
hostname travels with each request, so MagicDNS names resolve through the tailnet
without any system DNS change. The destination must still be on the tailnet (or
behind a subnet router) — tsnet removes the *client-side* app, not the
requirement that the other end is reachable.

## Architecture

One Go process owns everything. The seam:

```text
[frontend: stdio (default) | Ebitengine (-tags ebiten)]
   ↑ draws grid             ↓ key/mouse events
[terminal.Engine] ← VT bytes ← PTY ─┐
   pure-go VT (default)              │
   libghostty (-tags libghostty)     │
                          [localpty: $SHELL in a local PTY]
                                     │  ALL_PROXY / HTTP(S)_PROXY in its env
                          [proxy: SOCKS5 + HTTP on 127.0.0.1]
                                     │ every CONNECT dialed through ↓
                          [transport.Dialer]
                            tsnet.Server.Dial (default)
                            net.Dialer (-no-tailnet / tests)
                                     │ userspace WireGuard (gVisor netstack)
                                     ▼
                                 tailnet → host:port
```

Every layer is an interface so it can be swapped and tested in isolation:

| Layer | Package | Interface | Default impl | Alternate |
|---|---|---|---|---|
| Tailnet transport | `internal/transport` | `Dialer` | `TSNetDialer` (tsnet) | `NetDialer` (`-no-tailnet`, tests) |
| Egress proxy | `internal/proxy` | — | SOCKS5 + HTTP over the dialer conn | |
| Local shell | `internal/localpty` | — | `$SHELL` in a PTY (`creack/pty`) | |
| Terminal engine | `internal/terminal` | `Engine` | pure-Go VT parser | libghostty (`-tags libghostty`) |
| Frontend | `internal/render` | `Frontend` | stdio passthrough | Ebitengine (`-tags ebiten`), `Headless` (tests) |
| Controller | `internal/app` | — | wires shell ↔ frontend | |
| Config | `internal/config` | — | flags + env | |

## Install

### Homebrew

This repository doubles as its own Homebrew tap, so no separate `homebrew-wisp`
repo is involved:

```sh
brew install billzhuang/wisp/wisp
```

That installs the pure-Go **CLI** flavor (`wisp`). The release workflow
regenerates `Formula/wisp.rb` from the published checksums on every tagged
release (`cmd/genbrew`), so the formula always matches the verified release
binary — meaning `brew install` works from the first tagged release onward. The
GPU **GUI** app ships via the GitHub release and updates itself in-app (see
[Auto-update](#auto-update)).

### From a release / source

Download a binary from [Releases](https://github.com/billzhuang/wisp/releases),
or `go install github.com/billzhuang/wisp/cmd/wisp@latest` for the CLI.

## Usage

```sh
# Interactive login URL on first run; node identity persists in -state-dir.
# Opens your $SHELL with tailnet egress wired in.
wisp

# Inside the terminal, reach a tailnet/subnet-router resource as usual:
curl http://dev-box.internal/        # routed through the embedded tsnet node
git clone https://gitea.tailnet/...  # MagicDNS resolved over the tailnet

# Pre-authenticated / ephemeral node against a self-hosted control plane:
TS_AUTHKEY=tskey-... wisp -control-url https://headscale.example.com -ephemeral

# Headless/CI login the modern way — an OAuth client secret (scoped, revocable)
# instead of a long-lived auth key. wisp mints a short-lived key at startup; an
# OAuth-authenticated node must be tagged, so -tags is required:
TS_CLIENT_SECRET=tskey-client-... wisp -tags tag:ci -ephemeral

# Run a one-shot command instead of an interactive shell:
wisp -command 'curl -s http://dev-box.internal/health'

# Plain local terminal with no embedded Tailscale (proxy via the OS stack):
wisp -no-tailnet
```

Run `wisp -h` for the full flag list. The shell wisp launches is `$SHELL` (or
`-shell`); its environment carries `ALL_PROXY` / `HTTP_PROXY` / `HTTPS_PROXY`
pointing at the embedded proxy, plus `WISP=1` so prompts/scripts can tell they
are running inside wisp.

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

Updates don't accumulate disk: the swap keeps the previous binary as a
`.old` backup so a failed replace can roll back, and every launch reclaims
that backup (plus any temp file from an interrupted download) best-effort, so
the install directory holds a single binary across repeated upgrades.

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

The grid scrolls by rotating per-row references and recycling one row, so a
line feed costs O(rows) rather than an O(rows×cols) copy of the whole grid, and
the SGR colour palette is interned so coloured output doesn't allocate per
cell. (`go test -bench=EngineScroll ./internal/terminal/` measures the scroll
path on its own.)

Ghostty's "blazing fast" comes from a GPU glyph-cache renderer and a
SIMD-optimised VT parser written in Zig. wisp reaches that tier through its
pluggable backends, not by reimplementing them in pure Go:

- **`-tags libghostty`** swaps in the real Ghostty VT engine (libghostty-vt) —
  the same parser Ghostty ships — behind the identical `terminal.Engine`
  interface (see [docs/BUILD.md](docs/BUILD.md)).
- **`-tags ebiten`** renders the grid on the GPU rather than the CPU.

So the architecture is built for Ghostty-class speed; the pure-Go path is the
always-portable, zero-toolchain fallback.

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
  palette, config parsing/validation, transport, the egress proxy (HTTP CONNECT
  + SOCKS5), the local PTY session, the headless frontend.
- **Integration:** `internal/app/integration_test.go` spawns a real local shell
  on a PTY, pipes its output through the terminal engine, and asserts the
  rendered grid — exercising the whole shell → PTY → engine pipeline without a
  tailnet or a display.
- **End-to-end (black-box):** `internal/e2e/` drives the *actual compiled
  binary* the way a person does — launched as its own process with a real PTY,
  running a local shell (`-no-tailnet`), typed into, and asserted against the
  bytes it paints back (including that the proxy env was injected). This is the
  test that proves the shipped terminal works, not just that its packages pass
  unit tests. It is gated behind `-tags e2e`; an opt-in `TestLiveTailnet`
  additionally drives the real **tsnet** path — `curl`-ing a tailnet resource
  through the embedded proxy — when tailnet credentials are present. See
  [docs/TESTING.md](docs/TESTING.md).

One command runs the whole gauntlet (fmt, vet, race tests, build, e2e) and is
what the Claude Code auto-test loop calls each iteration:

```sh
scripts/autotest.sh            # run once; non-zero exit == wisp is broken
scripts/autotest.sh --loop     # repeat until a step fails (surfaces flakiness)
```

## Layout

```text
cmd/wisp/                main: flag parsing, dialer selection, proxy + shell wiring
internal/transport/      Dialer: tsnet node (no daemon) + plain net
internal/proxy/          SOCKS5 + HTTP egress proxy dialed through the tailnet
internal/localpty/       local $SHELL in a PTY (creack/pty)
internal/terminal/       Engine + pure-Go VT parser; libghostty scaffold
internal/terminal/palette ANSI/256/truecolour mapping
internal/render/         Frontend: stdio + Ebitengine + headless
internal/app/            Controller wiring the shell ↔ frontend
internal/config/         flags + env → validated Config
internal/update/         GitHub Releases self-update (check, verify, replace)
internal/version/        build version (ldflags-injected by CD)
internal/e2e/            black-box tests of the real binary over a PTY (-tags e2e)
scripts/autotest.sh      one-command test gauntlet for the auto-test loop
.github/workflows/       ci (build/test/vet/e2e) + release (tag → binaries + checksums)
docs/BUILD.md            libghostty (Zig) + Ebitengine toolchain notes
docs/TESTING.md          test scopes, the e2e harness, and the auto-test loop
```
