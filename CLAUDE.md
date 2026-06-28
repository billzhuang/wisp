# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What wisp is

wisp is a Tailscale-native terminal emulator. It embeds a **userspace Tailscale
node (tsnet)** in the binary and runs a **local shell** whose network egress is
routed through the tailnet — with no Tailscale app, daemon, or system client
installed, and without touching the host's DNS or routing. The terminal *is* the
tailnet node, so tools run in it (curl, git, Claude Code, Codex) reach tailnet
and subnet-router resources. The default build is pure Go (tsnet + a pure-Go VT
engine + a stdio frontend); the heavy components (libghostty VT engine,
Ebitengine GPU frontend) live behind build tags so the default build always
compiles with only the Go toolchain.

Note on history: wisp originally *SSHed* to a remote tailnet host. It was
reworked to a local shell + egress proxy so users don't have to install the
system Tailscale client (which adds a TUN device and rewrites DNS/routing,
conflicting with corporate VPN/DNS). There is no SSH code anymore.

## Commands

```sh
# Default build/test (pure Go: tsnet + pure-Go VT + stdio). Needs only Go 1.26+.
go build ./...
go test ./...
go test -race ./...

# One package / one test
go test ./internal/terminal/
go test -run TestEngineWrite ./internal/terminal/
go test -tags e2e -run TestLiveTailnet ./internal/e2e/...   # opt-in, see below

# Benchmarks (the README quotes these for perf claims)
go test -bench=EngineWrite ./internal/terminal/
go test -bench=EngineScroll ./internal/terminal/

# End-to-end: drives the actual compiled binary over a PTY (gated by the e2e tag)
go test -tags e2e -count=1 ./internal/e2e/...

# The full gauntlet — gofmt, vet, race tests, build, e2e — one non-zero exit == broken.
# This is exactly what the Claude Code auto-test loop runs each iteration.
scripts/autotest.sh
scripts/autotest.sh --quick    # skip e2e (fast inner loop while editing)
scripts/autotest.sh --loop     # repeat until a step fails (shakes out flakiness/races)

# Tagged builds need extra toolchains (see docs/BUILD.md):
go build -tags ebiten ./...        # GPU window frontend (cgo + GL/X11 dev headers)
go build -tags libghostty ./...    # real Ghostty VT engine (cgo against libghostty-vt)
```

`gofmt` cleanliness and `go vet` are enforced by both CI and `autotest.sh` — run
the gauntlet (or at least `gofmt -l .`) before considering a change done.

## Architecture: everything is a swappable seam

One Go process owns the whole pipeline. Each layer is an interface so it can be
replaced and tested in isolation, and the data flow is one direction down,
events back up:

```
frontend (render.Frontend)  ──input──▶  Controller ──▶ localpty ($SHELL on a PTY)
    ▲ draws grid each frame                                  │ ALL_PROXY/HTTP(S)_PROXY in its env
    └──── terminal.Engine ◀──── PTY bytes ◀──────────────────┘ point at ▼
                                              proxy (SOCKS5+HTTP, 127.0.0.1) ──▶ tsnet Dialer ──▶ tailnet
```

The recurring pattern across the codebase: **a small interface, a pure-Go
default impl that always builds, and an alternate impl selected by a build tag
or flag.** When adding a feature, find the seam and respect it rather than
threading a concrete type through.

| Seam | Package | Interface | Default | Alternate |
|---|---|---|---|---|
| Tailnet transport | `internal/transport` | `Dialer` | `TSNetDialer` (tsnet) | `NetDialer` (`-no-tailnet`, tests) |
| Egress proxy | `internal/proxy` | — | SOCKS5 + HTTP, every CONNECT dialed through `Dialer` | |
| Local shell | `internal/localpty` | — | `$SHELL` in a PTY (`creack/pty`) | |
| Terminal engine | `internal/terminal` | `Engine` (an `io.Writer`) | pure-Go VT parser | libghostty (`-tags libghostty`) |
| Frontend | `internal/render` | `Frontend` | stdio passthrough | Ebitengine (`-tags ebiten`); `Headless` (tests) |
| Session controller | `internal/app` | `render.Controller` | `*Controller` (one session) | `*Tabs` (N sessions, GUI only) |

Key boundaries:

- **`transport.Dialer`** hands back a raw `net.Conn`. The `proxy` layers SOCKS5
  and HTTP on top and neither it nor any test cares whether the conn came from a
  WireGuard tunnel or a plain TCP socket. This is why tests run hermetically:
  they swap in `NetDialer` (the same transport `-no-tailnet` uses).
- **The proxy is the bridge tsnet needs.** A userspace stack can't transparently
  capture other programs' traffic (no TUN), so `main` injects
  `ALL_PROXY`/`HTTP_PROXY`/`HTTPS_PROXY` (plus a `WISP=1` marker) into the
  shell's environment, pointing at the proxy's loopback address. One listener
  serves both protocols, distinguished by the first byte (`0x05` → SOCKS5, else
  HTTP), so the one address works as both `http://` and `socks5h://`. MagicDNS
  names resolve through `Dialer.Dial`, not local DNS.
- **`localpty.Session`** runs the shell on a PTY and exposes
  `Stdout`/`Input`/`Resize`/`Wait`/`Close`. Its `Stdout` reader translates the
  Linux PTY `EIO`-on-child-exit into a clean `io.EOF` so frontends see normal
  end-of-stream.
- **`terminal.Engine` is an `io.Writer`**, so the shell's stdout copies straight
  into it with `io.Copy`. A frontend reads `Engine.Snapshot()` (a caller-owned
  grid copy) each frame while the engine keeps mutating concurrently.
- **`render.Controller`** is the *subset* of `*app.Controller` a frontend needs
  (`Stdout`/`Input`/`Resize`). It is defined in `render` (not imported from
  `app`) to avoid an import cycle and to keep frontends fake-able.
- **Optional frontend capabilities are type-asserted, not added to the base
  interface**: `render.UpdatePrompter` (in-app "press Ctrl+U to update"),
  `render.TabCapable` / `render.TabController` (multi-session tabs). `main`
  builds a `*app.Tabs` only when the frontend asserts `TabCapable.SupportsTabs()`
  — the GUI; stdio/headless stay single-session.

`cmd/wisp/main.go` is the only place all of this is wired together: parse config
→ build dialer (tsnet, or `NetDialer` under `-no-tailnet`) → start the proxy →
build the proxy-augmented child env → `app.NewLocal` + `Start` the shell → pick
engine + frontend → `frontend.Run`. Read it first to orient.

## Conventions and non-obvious invariants

- **Secret-bearing flags must default to empty string, never `getenv(...)`.**
  `flag.PrintDefaults` echoes a non-empty default into `wisp -h`, which would
  leak the key/secret. `-authkey`, `-client-secret` get their env fallback
  (`TS_AUTHKEY`, `TS_CLIENT_SECRET`) applied *after* `flag.Parse` in
  `config.go`, not as a flag default. Preserve this if adding secret flags.
- **tsnet node identity persists in `-state-dir`** (default `~/.config/wisp/tsnet`).
  A restart *reuses the same tailnet machine* — tsnet finds existing state and
  re-registers under it, ignoring the auth key/secret. A new machine appears only
  with an empty/throwaway state dir or `TSNET_FORCE_LOGIN=1` (this is how CI stays
  clean). Don't assume each launch is a fresh node.
- **Auth: OAuth client secret is preferred over a long-lived auth key.** A
  `tskey-client-…` secret is exchanged for a short-lived tagged key at startup
  (`internal/transport/tsnet.go`). OAuth-minted nodes *must* be tagged, so
  `-tags` is required with `-client-secret` (enforced in `Config.Validate`).
- **The shell is launched as a login shell**: `main` sets `argv[0]` to
  `-<base>` (e.g. `-zsh`) so the user's profile is sourced. `-command` runs
  `$SHELL -c <cmd>` instead. `-no-tailnet` skips tsnet and backs the proxy with
  the OS network stack — used by the hermetic e2e tests and for a plain local
  terminal; tailnet-only resources are then unreachable.
- **Self-update is flavor-aware.** `cmd/wisp/flavor_*.go` set `assetFlavor`
  per build tag (`wisp` for CLI, `wisp-gui` for the Ebitengine build) so a GUI
  install only ever upgrades to a GUI asset. `internal/update` downloads the
  asset for the running OS/arch, verifies SHA-256 against the release checksums,
  and atomically swaps the binary (keeping a `.old` backup, reclaimed next launch).
- **`internal/terminal/libghostty.go` is a scaffold**: it satisfies `Engine` and
  currently *falls back to the pure-Go engine* so the `-tags libghostty` build
  links and runs before the real cgo binding is wired. `terminal.Backend`
  reports which engine is active (printed at startup and by `wisp -version`).

## Testing model (see docs/TESTING.md)

Three widening scopes, all hermetic except one opt-in:

- **Unit** (`internal/*/*_test.go`) — each layer alone; Go toolchain only.
  Includes the egress proxy (HTTP CONNECT + SOCKS5 against a local echo server)
  and the local PTY session.
- **Integration** (`internal/app/integration_test.go`) — the layers *compose*:
  a real local shell → PTY → engine → grid, asserted against the rendered
  snapshot.
- **End-to-end** (`internal/e2e/`, `-tags e2e`) — compiles the real binary,
  attaches a PTY, launches a local shell with `-no-tailnet`, types, and asserts
  the painted bytes (including that the proxy env was injected). This is the test
  that proves "wisp works as a terminal." An opt-in `TestLiveTailnet` drives the
  real tsnet path — `curl`-ing a tailnet resource through the embedded proxy —
  when `WISP_E2E_*` credentials are present (it *skips* otherwise, e.g. fork PRs).

CI (`.github/workflows/ci.yml`) runs these as separate jobs: pure-Go test/race,
black-box e2e, an Ebitengine Linux smoke build under `xvfb`, and a
compile-check of the GUI on macOS Apple Silicon (the release target).

## Release flow

`.github/workflows/release.yml`: pushing a `vX.Y.Z` tag builds the binaries and
a `checksums.txt`, publishes a GitHub Release, and runs `cmd/genbrew` to
regenerate `Formula/wisp.rb` from the published checksums (this repo is its own
Homebrew tap). The version is stamped into the binary at link time via ldflags
into `internal/version`; `internal/update` reads exactly those asset names, so a
tagged release is simultaneously a `brew install` source and a one-click
in-app upgrade.
