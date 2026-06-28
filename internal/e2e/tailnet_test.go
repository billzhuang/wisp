//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveTailnet is the only test that exercises wisp's reason for existing:
// the embedded userspace Tailscale node (tsnet) coming up and dialing a real
// host over the tailnet — the exact path the hermetic localhost tests skip with
// -direct. It cannot be hermetic (it needs a tailnet + a reachable host), so it
// is opt-in: it runs only when the environment supplies credentials, and skips
// cleanly otherwise. In CI those come from GitHub Action secrets; locally,
// export them before `go test -tags e2e`.
//
// Authentication uses a Tailscale OAuth client secret, not a long-lived auth
// key. wisp's tsnet exchanges the secret for a short-lived, tagged auth key at
// startup (see internal/transport/tsnet.go), so nothing durable is stored — the
// OAuth client is scoped to a tag and revocable from the admin console, which is
// the modern recommended way to authenticate headless nodes.
//
// Required env:
//
//	WISP_E2E_TS_CLIENT_SECRET   OAuth client secret (tskey-client-…), auth_keys scope
//	WISP_E2E_TS_TAGS            comma-separated ACL tags the client owns (e.g. tag:ci)
//	WISP_E2E_HOST               destination host on the tailnet (host or host:port)
//	WISP_E2E_USER               remote login user
//
// One SSH auth method, in precedence order:
//
//	WISP_E2E_SSH_KEY            path to a private key for public-key auth (preferred), or
//	WISP_E2E_PASSWORD           password, typed into the interactive prompt over the PTY
//
// Optional env:
//
//	WISP_E2E_CONTROL_URL        coordination server (Headscale/self-hosted control plane)
//
// The test runs a single remote command via -command and asserts a unique token
// round-trips back through tsnet -> SSH -> engine -> frontend to the terminal.
func TestLiveTailnet(t *testing.T) {
	clientSecret := os.Getenv("WISP_E2E_TS_CLIENT_SECRET")
	tags := os.Getenv("WISP_E2E_TS_TAGS")
	host := os.Getenv("WISP_E2E_HOST")
	user := os.Getenv("WISP_E2E_USER")
	if clientSecret == "" || tags == "" || host == "" || user == "" {
		t.Skip("live tailnet test skipped: set WISP_E2E_TS_CLIENT_SECRET, WISP_E2E_TS_TAGS, WISP_E2E_HOST, WISP_E2E_USER to enable")
	}

	keyFile := os.Getenv("WISP_E2E_SSH_KEY")
	password := os.Getenv("WISP_E2E_PASSWORD")
	if keyFile == "" && password == "" {
		t.Fatal("live tailnet test enabled but no SSH auth provided: set WISP_E2E_SSH_KEY or WISP_E2E_PASSWORD")
	}

	// A unique token so we match the command's *output*, not the command echo.
	token := "WISP-LIVE-" + time.Now().Format("20060102-150405.000000")

	stateDir, err := os.MkdirTemp("", "wisp-e2e-tsnet")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(stateDir) })

	knownHosts := stateDir + "/known_hosts" // trust-on-first-use into a throwaway file

	args := []string{
		"-no-update-check",
		"-ephemeral", // node disappears from the tailnet when wisp exits
		"-hostname", "wisp-e2e",
		"-state-dir", stateDir,
		"-tags", tags, // OAuth-minted nodes must be tagged
		"-known-hosts", knownHosts,
		"-host", host,
		"-user", user,
		"-command", "echo " + token,
	}
	if cu := os.Getenv("WISP_E2E_CONTROL_URL"); cu != "" {
		args = append(args, "-control-url", cu)
	}
	if keyFile != "" {
		args = append(args, "-i", keyFile)
	}

	// Pass the OAuth secret via the environment (TS_CLIENT_SECRET), not argv, so
	// it never appears in a process listing. wisp reads it as -client-secret's
	// default.
	s := startWisp(t, []string{"TS_CLIENT_SECRET=" + clientSecret}, args...)

	if keyFile == "" && password != "" {
		// The interactive prompt reads one line from the PTY.
		s.write(password + "\n")
	}

	// tsnet bring-up (OAuth key mint + tailnet dial) is slower than a localhost
	// connection; allow generous headroom before declaring failure.
	out := s.waitFor(token, 120*time.Second)
	if !strings.Contains(out, token) {
		t.Fatalf("expected live remote output to contain %q; got:\n%s", token, sanitize(out))
	}
}
