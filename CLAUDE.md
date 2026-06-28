# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What wisp is

wisp is a Tailscale-native terminal emulator. It embeds a **userspace Tailscale
node (tsnet)** in the binary and SSHes to tailnet hosts with no Tailscale app,
daemon, or system client installed — the terminal *is* the tailnet node. The
default build is pure Go (tsnet + a pure-Go VT engine + a stdio frontend); the
heavy components (libghostty VT engine, Ebitengine GPU frontend) live behind
build tags so the default build always compiles with only the Go toolchain.

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
frontend (render.Frontend)  ──input──▶  Controller ──▶ SSH session ──▶ tsnet Dialer ──▶ tailnet host:22
    ▲ draws grid each frame                                                                    │
    └──── terminal.Engine ◀──── remote PTY bytes ◀──────────────────────────────────────────┘
```

The recurring pattern across the codebase: **a small interface, a pure-Go
default impl that always builds, and an alternate impl selected by a build tag
or flag.** When adding a feature, find the seam and respect it rather than
threading a concrete type through.

| Seam | Package | Interface | Default | Alternate |
|---|---|---|---|---|
| Tailnet transport | `internal/transport` | `Dialer` | `TSNetDialer` (tsnet) | `NetDialer` (`-direct`, tests) |
| SSH session | `internal/sshx` | — | `x/crypto/ssh` over the dialer's `net.Conn` | |
| Terminal engine | `internal/terminal` | `Engine` (an `io.Writer`) | pure-Go VT parser | libghostty (`-tags libghostty`) |
| Frontend | `internal/render` | `Frontend` | stdio passthrough | Ebitengine (`-tags ebiten`); `Headless` (tests) |
| Session controller | `internal/app` | `render.Controller` | `*Controller` (one session) | `*Tabs` (N sessions, GUI only) |

Key boundaries:

- **`transport.Dialer`** hands back a raw `net.Conn`. `sshx` layers SSH on top
  and neither it nor any test cares whether the conn came from a WireGuard
  tunnel or a plain TCP socket. This is why tests run hermetically: they swap in
  `NetDialer` against an in-process SSH server.
- **`terminal.Engine` is an `io.Writer`**, so a session's stdout copies straight
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
→ build dialer → build host-key callback + auth → `app.Dial` → pick engine +
frontend → `frontend.Run`. Read it first to orient.

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
- **Host keys are trust-on-first-use**: unknown hosts are recorded in
  `known_hosts`, a *changed* key is always rejected as a possible MITM.
  `-insecure-host-key` exists for tests only.
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
- **Integration** (`internal/app/integration_test.go`, `internal/sshx/sshx_test.go`)
  — the layers *compose*: `NetDialer` → SSH → PTY → engine → grid, asserted
  against the rendered snapshot, via the in-process `internal/testutil/sshserver`.
- **End-to-end** (`internal/e2e/`, `-tags e2e`) — compiles the real binary,
  attaches a PTY, connects to the test SSH server, types, and asserts the painted
  bytes. This is the test that proves "wisp works as a terminal." An opt-in
  `TestLiveTailnet` additionally drives the real tsnet path when `WISP_E2E_*`
  credentials are present (it *skips* otherwise — e.g. on fork PRs).

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
