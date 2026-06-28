//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveTailnet is the only test that exercises wisp's reason for existing:
// the embedded userspace Tailscale node (tsnet) coming up and a program in the
// local shell reaching a real tailnet resource *through the embedded egress
// proxy* — the exact path the hermetic tests skip with -no-tailnet. It cannot be
// hermetic (it needs a tailnet + a reachable HTTP resource), so it is opt-in: it
// runs only when the environment supplies credentials, and skips cleanly
// otherwise. In CI those come from GitHub Action secrets; locally, export them
// before `go test -tags e2e`.
//
// Authentication uses a Tailscale OAuth client secret, not a long-lived auth
// key. wisp's tsnet exchanges the secret for a short-lived, tagged auth key at
// startup (see internal/transport/tsnet.go), so nothing durable is stored — the
// OAuth client is scoped to a tag and revocable from the admin console.
//
// Required env:
//
//	WISP_E2E_TS_CLIENT_SECRET   OAuth client secret (tskey-client-…), auth_keys scope
//	WISP_E2E_TS_TAGS            comma-separated ACL tags the client owns (e.g. tag:ci)
//	WISP_E2E_URL                an HTTP URL reachable only over the tailnet
//	WISP_E2E_EXPECT             a substring expected in that URL's response body
//
// Optional env:
//
//	WISP_E2E_CONTROL_URL        coordination server (Headscale/self-hosted control plane)
//
// The test runs `curl` in the shell, pointed at the URL through the proxy env
// vars wisp injects, and asserts the expected token round-trips back through
// proxy -> tsnet -> tailnet -> engine -> frontend to the terminal.
func TestLiveTailnet(t *testing.T) {
	clientSecret := os.Getenv("WISP_E2E_TS_CLIENT_SECRET")
	tags := os.Getenv("WISP_E2E_TS_TAGS")
	url := os.Getenv("WISP_E2E_URL")
	expect := os.Getenv("WISP_E2E_EXPECT")
	if clientSecret == "" || tags == "" || url == "" || expect == "" {
		t.Skip("live tailnet test skipped: set WISP_E2E_TS_CLIENT_SECRET, WISP_E2E_TS_TAGS, WISP_E2E_URL, WISP_E2E_EXPECT to enable")
	}

	stateDir, err := os.MkdirTemp("", "wisp-e2e-tsnet")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(stateDir) })

	// curl honors $ALL_PROXY / $HTTP_PROXY, which wisp injects pointing at the
	// embedded proxy; --max-time keeps a misconfigured run from hanging the test.
	cmd := "curl -sS --max-time 60 " + shellQuote(url) + " || echo WISP-CURL-FAILED"

	args := []string{
		"-no-update-check",
		"-ephemeral", // node disappears from the tailnet when wisp exits
		"-hostname", "wisp-e2e",
		"-state-dir", stateDir,
		"-tags", tags, // OAuth-minted nodes must be tagged
		"-command", cmd,
	}
	if cu := os.Getenv("WISP_E2E_CONTROL_URL"); cu != "" {
		args = append(args, "-control-url", cu)
	}

	// Pass the OAuth secret via the environment (TS_CLIENT_SECRET), not argv, so
	// it never appears in a process listing.
	s := startWisp(t, []string{"TS_CLIENT_SECRET=" + clientSecret}, args...)

	// tsnet bring-up (OAuth key mint + tailnet dial) is slower than a localhost
	// connection; allow generous headroom before declaring failure.
	out := s.waitFor(expect, 120*time.Second)
	if !strings.Contains(out, expect) {
		t.Fatalf("expected live tailnet response to contain %q; got:\n%s", expect, sanitize(out))
	}
}

// shellQuote wraps s in single quotes for safe use in `sh -c`, escaping any
// embedded single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
