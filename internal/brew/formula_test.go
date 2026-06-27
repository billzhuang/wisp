package brew

import (
	"strings"
	"testing"
)

func TestFormulaContents(t *testing.T) {
	got := Formula(Params{
		Version: "1.2.0",
		URL:     "https://github.com/billzhuang/wisp/releases/download/v1.2.0/wisp_darwin_arm64",
		SHA256:  "abc123",
	})
	wants := []string{
		"class Wisp < Formula",
		`version "1.2.0"`,
		`url "https://github.com/billzhuang/wisp/releases/download/v1.2.0/wisp_darwin_arm64"`,
		`sha256 "abc123"`,
		`bin.install "wisp_darwin_arm64" => "wisp"`,
		`shell_output("#{bin}/wisp -version")`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("formula missing %q\n---\n%s", w, got)
		}
	}
	// A formula must end with the closing `end` so it parses.
	if !strings.HasSuffix(strings.TrimSpace(got), "end") {
		t.Errorf("formula does not end with `end`:\n%s", got)
	}
}

func TestSHA256For(t *testing.T) {
	checksums := "deadbeef  wisp-gui_darwin_arm64\n" +
		"CAFEBABE *wisp_darwin_arm64\n" + // binary-mode "*" and upper-case hex
		"garbage line with too many fields here\n"
	got, err := SHA256For(checksums, "wisp_darwin_arm64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "cafebabe" { // trimmed "*" and lower-cased
		t.Fatalf("sha = %q, want %q", got, "cafebabe")
	}

	if other, _ := SHA256For(checksums, "wisp-gui_darwin_arm64"); other != "deadbeef" {
		t.Fatalf("gui sha = %q, want deadbeef", other)
	}
}

// TestSHA256ForCRLF guards against a checksums file with CRLF line endings: the
// trailing "\r" must not end up in the parsed name (defeating the match) or the
// returned hash. strings.Fields treats "\r" as whitespace, so this works, but
// the test pins that behaviour.
func TestSHA256ForCRLF(t *testing.T) {
	got, err := SHA256For("dead  wisp-gui_darwin_arm64\r\nbeef  wisp_darwin_arm64\r\n", "wisp_darwin_arm64")
	if err != nil {
		t.Fatal(err)
	}
	if got != "beef" {
		t.Fatalf("sha = %q, want %q (CRLF should not affect parsing)", got, "beef")
	}
}

func TestSHA256ForMissing(t *testing.T) {
	if _, err := SHA256For("abc  other_asset\n", "wisp_darwin_arm64"); err == nil {
		t.Fatal("expected an error when the asset is absent")
	}
}

// TestFormulaUsesParsedChecksum is the end-to-end path the release workflow
// runs: pull the sha from a checksums file, then render the formula with it.
func TestFormulaUsesParsedChecksum(t *testing.T) {
	checksums := "111aaa  wisp-gui_darwin_arm64\n222bbb  wisp_darwin_arm64\n"
	sha, err := SHA256For(checksums, "wisp_darwin_arm64")
	if err != nil {
		t.Fatal(err)
	}
	f := Formula(Params{Version: "2.0.0", URL: "https://example/wisp_darwin_arm64", SHA256: sha})
	if !strings.Contains(f, `sha256 "222bbb"`) {
		t.Fatalf("rendered formula did not carry the parsed checksum:\n%s", f)
	}
}
