// Command genbrew renders the Homebrew tap formula (Formula/wisp.rb) for a
// release. The release workflow runs it on each tag with the version, the CLI
// binary's download URL, and its SHA-256 — read either directly (-sha) or from
// the release checksums file (-checksums). Keeping the formula generated from a
// single source of truth means it can't drift from the published assets.
//
// Usage:
//
//	genbrew -version 1.2.0 -url https://.../wisp_darwin_arm64 -sha <hex> -out Formula/wisp.rb
//	genbrew -version 1.2.0 -url https://.../wisp_darwin_arm64 -checksums wisp_1.2.0_checksums.txt
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/billzhuang/wisp/internal/brew"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "genbrew:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("genbrew", flag.ContinueOnError)
	var p brew.Params
	var out, checksums, asset string
	fs.StringVar(&p.Version, "version", "", "release version without a leading v (required)")
	fs.StringVar(&p.URL, "url", "", "download URL of the CLI binary (required)")
	fs.StringVar(&p.SHA256, "sha", "", "SHA-256 of the binary; or use -checksums")
	fs.StringVar(&checksums, "checksums", "", "path to a checksums file to read the SHA from")
	fs.StringVar(&asset, "asset", "wisp_darwin_arm64", "asset name to look up in -checksums")
	fs.StringVar(&out, "out", "Formula/wisp.rb", "output path for the formula")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if p.SHA256 == "" && checksums != "" {
		b, err := os.ReadFile(checksums)
		if err != nil {
			return err
		}
		sha, err := brew.SHA256For(string(b), asset)
		if err != nil {
			return err
		}
		p.SHA256 = sha
	}
	if p.Version == "" || p.URL == "" || p.SHA256 == "" {
		return fmt.Errorf("-version, -url and -sha (or -checksums) are required")
	}
	// The formula is committed to the tap's default branch, so a malformed value
	// would break `brew install` for everyone. Reject anything that can't sit in
	// a double-quoted Ruby string (quote/newline/tab) and fail the release step
	// rather than emit a broken formula. Inputs come from the tag and checksums
	// file, so this should never trip — it's a guardrail, not expected flow.
	if err := validate(p); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, []byte(brew.Formula(p)), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "genbrew: wrote %s for %s\n", out, p.Version)
	return nil
}

// validate rejects field values that can't sit safely inside a double-quoted
// Ruby string in the rendered formula: a quote or newline would break out of
// the literal, a backslash starts an escape, and "#{" begins interpolation.
func validate(p brew.Params) error {
	for _, f := range []struct{ name, val string }{
		{"version", p.Version}, {"url", p.URL}, {"sha", p.SHA256},
	} {
		if strings.ContainsAny(f.val, "\"\\\n\r\t") || strings.Contains(f.val, "#{") {
			return fmt.Errorf("%s contains an illegal character: %q", f.name, f.val)
		}
	}
	return nil
}
