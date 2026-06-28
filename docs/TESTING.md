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
it **skips** unless credentials are in the environment.

Authentication uses a Tailscale **OAuth client secret**, *not* a long-lived auth
key. wisp's tsnet exchanges the secret for a short-lived, tagged key at startup
(see [`internal/transport/tsnet.go`](../internal/transport/tsnet.go)), so nothing
durable is stored. This is Tailscale's recommended way to authenticate headless
nodes: the OAuth client is scoped to a tag and revocable from the admin console.

| Env var | Required? | Meaning |
|---|---|---|
| `WISP_E2E_TS_CLIENT_SECRET` | yes | OAuth client secret (`tskey-client-…`) with the `auth_keys` scope |
| `WISP_E2E_TS_TAGS` | yes | comma-separated ACL tag(s) the client owns, e.g. `tag:ci` |
| `WISP_E2E_HOST` | yes | destination host on the tailnet (`host` or `host:port`) |
| `WISP_E2E_USER` | yes | remote login user |
| `WISP_E2E_SSH_KEY` | one of | path to a private key (public-key auth, preferred) |
| `WISP_E2E_PASSWORD` | one of | password, typed into the prompt over the PTY |
| `WISP_E2E_CONTROL_URL` | no | Headscale / self-hosted control plane URL |

#### Creating the OAuth client (one-time)

1. In the Tailscale admin console, define a tag (e.g. `tag:ci`) in your ACLs and
   grant SSH to your test host for that tag.
2. **Settings → OAuth clients → Generate** a client with the **`auth_keys`**
   write scope, attached to `tag:ci`.
3. Store the generated secret as `WISP_E2E_TS_CLIENT_SECRET`. Revoke it any time
   from the same screen; it grants nothing beyond minting keys for that tag.

#### What the minted key looks like — and node identity on restart

When wisp's tsnet sees a `tskey-client-…` secret it mints a fresh auth key with
defaults **`ephemeral=true`, `preauthorized=false`**. You can override them
inline on the secret: `tskey-client-…?preauthorized=true&ephemeral=true`.

- **`ephemeral=true`** (default) is what keeps CI clean: each run registers a new
  node that the control plane **auto-removes** minutes after wisp exits. The test
  pairs this with a throwaway `-state-dir` and `-ephemeral`, so runs never
  accumulate machines in the admin console.
- **`preauthorized=true`** is worth adding if your tailnet has **device
  approval** enabled — otherwise the freshly minted node sits unapproved and the
  test times out waiting to come online.

This is the flip side of how wisp behaves for real users: node identity lives in
`-state-dir` (default `~/.config/wisp/tsnet`), which **persists**. A normal user
restarting wisp **reuses the same machine** — tsnet finds the existing state and
re-registers under that identity, ignoring the auth key/secret entirely. A *new*
machine appears only when the state dir is empty/throwaway (as in CI) or
`TSNET_FORCE_LOGIN=1` is set. So: persistent state dir → reuse; ephemeral +
fresh state dir → new, self-cleaning node per run.

```sh
export WISP_E2E_TS_CLIENT_SECRET=tskey-client-...
export WISP_E2E_TS_TAGS=tag:ci
export WISP_E2E_HOST=dev-box
export WISP_E2E_USER=alice
export WISP_E2E_SSH_KEY=~/.ssh/id_ed25519
go test -tags e2e -run TestLiveTailnet ./internal/e2e/...
```

In CI these come from repository **secrets** (`WISP_E2E_*`), wired into the
`e2e` job in [`.github/workflows/ci.yml`](../.github/workflows/ci.yml). The
secret is passed to wisp through the environment (`TS_CLIENT_SECRET`), never on
the command line, so it can't surface in a process listing. On fork PRs — where
secrets are unavailable — the live test simply skips, and the hermetic localhost
tests still run.

> Even more keyless: tsnet also supports **workload identity federation** (the
> runner's GitHub OIDC token, no stored secret at all), but that path links the
> `tailscale.com/feature/identityfederation` package, which pulls the AWS SDK
> into the binary. wisp keeps that out of the default build, so OAuth — one
> scoped, revocable secret — is the recommended approach here.

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
