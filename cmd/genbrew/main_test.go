package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWritesFormulaFromChecksums(t *testing.T) {
	dir := t.TempDir()
	sums := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(sums, []byte("aaa  wisp-gui_darwin_arm64\nbbb  wisp_darwin_arm64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "Formula", "wisp.rb") // nested dir must be created

	err := run([]string{
		"-version", "1.4.0",
		"-url", "https://example/wisp_darwin_arm64",
		"-checksums", sums,
		"-out", out,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{`version "1.4.0"`, `sha256 "bbb"`, "class Wisp < Formula"} {
		if !strings.Contains(string(got), w) {
			t.Errorf("formula missing %q:\n%s", w, got)
		}
	}
}

func TestRunRequiresFields(t *testing.T) {
	if err := run([]string{"-version", "1.0.0"}); err == nil {
		t.Fatal("expected error when url/sha are missing")
	}
}

func TestRunRejectsInjection(t *testing.T) {
	out := filepath.Join(t.TempDir(), "wisp.rb")
	// A sha that closes the Ruby string and injects code must be rejected, not
	// written out, so a broken formula never reaches the tap.
	err := run([]string{
		"-version", "1.0.0",
		"-url", "https://example/wisp_darwin_arm64",
		"-sha", "abc\"\nsystem('boom')",
		"-out", out,
	})
	if err == nil {
		t.Fatal("expected validation error for sha with quote/newline")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("a rejected formula must not be written")
	}
}
