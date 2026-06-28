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
// Required env:
//
//	WISP_E2E_TS_AUTHKEY   tsnet auth key (tskey-...) — registers the ephemeral node
//	WISP_E2E_HOST         destination host on the tailnet (host or host:port)
//	WISP_E2E_USER         remote login user
//
// One auth method, in precedence order:
//
//	WISP_E2E_SSH_KEY      path to a private key for public-key auth (preferred), or
//	WISP_E2E_PASSWORD     password, typed into the interactive prompt over the PTY
//
// Optional env:
//
//	WISP_E2E_CONTROL_URL  coordination server (Headscale/self-hosted control plane)
//
// The test runs a single remote command via -command and asserts a unique token
// round-trips back through tsnet -> SSH -> engine -> frontend to the terminal.
func TestLiveTailnet(t *testing.T) {
	authKey := os.Getenv("WISP_E2E_TS_AUTHKEY")
	host := os.Getenv("WISP_E2E_HOST")
	user := os.Getenv("WISP_E2E_USER")
	if authKey == "" || host == "" || user == "" {
		t.Skip("live tailnet test skipped: set WISP_E2E_TS_AUTHKEY, WISP_E2E_HOST, WISP_E2E_USER to enable")
	}

	keyFile := os.Getenv("WISP_E2E_SSH_KEY")
	password := os.Getenv("WISP_E2E_PASSWORD")
	if keyFile == "" && password == "" {
		t.Fatal("live tailnet test enabled but no auth provided: set WISP_E2E_SSH_KEY or WISP_E2E_PASSWORD")
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
		"-authkey", authKey,
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

	s := startWisp(t, args...)

	if keyFile == "" && password != "" {
		// The interactive prompt reads one line from the PTY.
		s.write(password + "\n")
	}

	// tsnet bring-up + tailnet dial is slower than a localhost connection;
	// allow generous headroom before declaring failure.
	out := s.waitFor(token, 90*time.Second)
	if !strings.Contains(out, token) {
		t.Fatalf("expected live remote output to contain %q; got:\n%s", token, sanitize(out))
	}
}
