// Package brew renders the Homebrew formula that lets users install the wisp
// CLI with `brew install billzhuang/wisp/wisp`.
//
// Committing the rendered file to Formula/wisp.rb on the default branch makes
// this repository itself a Homebrew tap, so no separate "homebrew-wisp" repo is
// needed. The release workflow regenerates the formula on every tag from the
// release's checksums file (see cmd/genbrew); the formula is just data, so the
// rendering is kept here behind a pure function and unit-tested in isolation.
//
// brew installs the pure-Go CLI flavor (wisp_darwin_arm64). The GPU GUI flavor
// continues to ship via the GitHub release and the in-app updater.
package brew

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// Params are the per-release inputs to the formula.
type Params struct {
	// Version is the release version without a leading "v", e.g. "1.2.0".
	Version string
	// URL is the download URL of the darwin/arm64 CLI binary.
	URL string
	// SHA256 is the hex SHA-256 of that binary, from the release checksums file.
	SHA256 string
}

// binaryName is the released CLI asset brew installs as "wisp". It is also the
// filename Homebrew stages the bare-binary download under, so `bin.install`
// renames it in place.
const binaryName = "wisp_darwin_arm64"

// Formula renders a Homebrew formula installing the wisp CLI from the published
// release binary and verifying its SHA-256. The returned string is the complete
// contents of Formula/wisp.rb.
func Formula(p Params) string {
	return fmt.Sprintf(`class Wisp < Formula
  desc "Tailscale-native terminal: local shell with embedded tsnet egress, no daemon"
  homepage "https://github.com/billzhuang/wisp"
  version "%s"
  url "%s"
  sha256 "%s"

  def install
    bin.install "%s" => "wisp"
  end

  test do
    assert_match "wisp", shell_output("#{bin}/wisp -version")
  end
end
`, p.Version, p.URL, p.SHA256, binaryName)
}

// SHA256For extracts the hex SHA-256 for asset from the contents of a
// shasum/sha256sum-style checksums file, whose lines are "<hex>  <filename>"
// (the filename may be prefixed with "*" in binary mode). It is the same format
// the release workflow publishes and the in-app updater consumes.
func SHA256For(checksums, asset string) (string, error) {
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == asset {
			// Fail fast on a truncated or non-hex digest rather than letting a
			// bogus value land in the committed formula.
			sha := strings.ToLower(fields[0])
			if len(sha) != 64 {
				return "", fmt.Errorf("brew: %q checksum is not a 64-char SHA-256: %q", asset, fields[0])
			}
			if _, err := hex.DecodeString(sha); err != nil {
				return "", fmt.Errorf("brew: %q checksum is not hex: %q", asset, fields[0])
			}
			return sha, nil
		}
	}
	return "", fmt.Errorf("brew: no checksum entry for %q", asset)
}
