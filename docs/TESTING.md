# Testing wisp

wisp is a terminal, so "does it work?" ultimately means "when a person launches
the binary and types, do the right bytes appear on screen?" The test suite
answers that at three widening scopes, plus a one-command loop that ties them
together.

## The three scopes

| Scope | Where | What it proves | Needs |
|---|---|---|---|
| **Unit** | `internal/*/*_test.go` | each layer in isolation — VT parser, palette, config, transport, host-key TOFU, headless frontend | Go toolchain only |
| **Integration (in-process)** | `internal/app/integration_test.go`, `internal/sshx/sshx_test.go` | the layers *compose*: NetDialer → SSH → PTY → engine → grid, asserted against the rendered snapshot | Go toolchain only |
| **End-to-end (black-box)** | `internal/e2e/` | the *shipped binary* connects, renders, forwards keystrokes, and runs remote commands — driven over a real PTY exactly as a user would | Go toolchain + PTY (any Linux/macOS) |

The first two run under the default `go test ./...`. The third is gated behind
the `e2e` build tag so the default run stays fast and hermetic.

## End-to-end: testing the real binary

`internal/e2e` is the piece that catches what unit tests cannot — flag parsing,
the interactive auth prompt, raw-mode terminal handling, and the stdio frontend
all only exist in the assembled binary. The harness:

1. compiles `cmd/wisp` once (the default pure-Go / stdio flavor),
2. starts the in-process test SSH server (`internal/testutil/sshserver`) on
   localhost,
3. launches `wisp -direct -insecure-host-key …` as its own process with a real
   PTY for stdio (via `github.com/creack/pty`),
4. types into the PTY and asserts on the bytes wisp paints back.

No tailnet, display, or system sshd is involved, so it runs unchanged in CI.

```sh
go test -tags e2e -count=1 ./internal/e2e/...
```

### Live tailnet test (opt-in)

`-direct` is what the hermetic tests use, and it bypasses wisp's whole reason
for existing: the embedded **tsnet** node. `internal/e2e/tailnet_test.go` covers
that real path, but it needs a tailnet and a reachable host, so it is opt-in —
it **skips** unless credentials are in the environment:

| Env var | Required? | Meaning |
|---|---|---|
| `WISP_E2E_TS_AUTHKEY` | yes | tsnet auth key (`tskey-…`); the node registers ephemerally |
| `WISP_E2E_HOST` | yes | destination host on the tailnet (`host` or `host:port`) |
| `WISP_E2E_USER` | yes | remote login user |
| `WISP_E2E_SSH_KEY` | one of | path to a private key (public-key auth, preferred) |
| `WISP_E2E_PASSWORD` | one of | password, typed into the prompt over the PTY |
| `WISP_E2E_CONTROL_URL` | no | Headscale / self-hosted control plane URL |

```sh
export WISP_E2E_TS_AUTHKEY=tskey-...
export WISP_E2E_HOST=dev-box
export WISP_E2E_USER=alice
export WISP_E2E_SSH_KEY=~/.ssh/id_ed25519
go test -tags e2e -run TestLiveTailnet ./internal/e2e/...
```

In CI these come from repository **secrets** (`WISP_E2E_*`), wired into the
`e2e` job in [`.github/workflows/ci.yml`](../.github/workflows/ci.yml). On fork
PRs — where secrets are unavailable — the live test simply skips, and the
hermetic localhost tests still run.

## The auto-test loop

[`scripts/autotest.sh`](../scripts/autotest.sh) is the single command that runs
the whole gauntlet — gofmt, vet, `go test -race ./...`, build, and the e2e
suite — and exits non-zero on the first failure. It is what the **Claude Code
auto-test loop** invokes each iteration: one clean exit code means "wisp works",
one non-zero means "here is the broken step, fix it".

```sh
scripts/autotest.sh            # run the gauntlet once
scripts/autotest.sh --quick    # skip e2e (fast inner loop while editing)
scripts/autotest.sh --loop     # repeat until something fails (surfaces flakiness/races)
scripts/autotest.sh --loop 20  # repeat at most 20 times
```

### Driving it from Claude Code

To have Claude Code build-and-verify wisp on a loop, point its `/loop` at the
script:

```
/loop 10m scripts/autotest.sh
```

Each tick runs the gauntlet; on a non-zero exit Claude has the failing step and
output in context and can fix it before the next tick. `--loop` mode inside the
script is the complementary tool for shaking out *flaky* failures (races,
timing) without re-spawning a session each time.
