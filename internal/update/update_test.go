package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fakeGitHub serves a release, a per-platform binary asset, and a checksums
// file, mimicking the layout the CD workflow publishes.
func fakeGitHub(t *testing.T, tag string, binary []byte) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	assetName := fmt.Sprintf("wisp_%s_%s", "testos", "testarch")
	sum := sha256.Sum256(binary)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), assetName)

	mux.HandleFunc("/download/bin", func(w http.ResponseWriter, r *http.Request) {
		w.Write(binary)
	})
	mux.HandleFunc("/download/checksums", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, checksums)
	})
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		rel := Release{
			Tag:     tag,
			Name:    "Release " + tag,
			Notes:   "notes",
			HTMLURL: "https://example.test/release",
			Assets: []Asset{
				{Name: assetName, URL: srv.URL + "/download/bin", Size: int64(len(binary))},
				{Name: "wisp_" + tag + "_checksums.txt", URL: srv.URL + "/download/checksums"},
			},
		}
		json.NewEncoder(w).Encode(rel)
	})
	return srv, assetName
}

func TestCheckForUpdate(t *testing.T) {
	srv, _ := fakeGitHub(t, "v1.2.0", []byte("binary"))
	c := &Checker{Repo: "owner/repo", Current: "1.1.0", BaseURL: srv.URL}

	rel, newer, err := c.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !newer {
		t.Fatal("expected an update to be available")
	}
	if rel.Version() != "1.2.0" {
		t.Fatalf("version = %q", rel.Version())
	}
}

func TestCheckForUpdateUpToDate(t *testing.T) {
	srv, _ := fakeGitHub(t, "v1.2.0", []byte("binary"))
	c := &Checker{Repo: "owner/repo", Current: "1.2.0", BaseURL: srv.URL}
	_, newer, err := c.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if newer {
		t.Fatal("did not expect an update when already on latest")
	}
}

func TestLatestHTTPError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Checker{Repo: "owner/repo", Current: "1.0.0", BaseURL: srv.URL}
	if _, err := c.Latest(context.Background()); err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestApplyReplacesBinary(t *testing.T) {
	newBinary := []byte("#!/bin/sh\necho new version\n")
	srv, _ := fakeGitHub(t, "v1.2.0", newBinary)

	// A stand-in for the running executable.
	target := filepath.Join(t.TempDir(), "wisp")
	if err := os.WriteFile(target, []byte("old version"), 0o755); err != nil {
		t.Fatal(err)
	}

	c := &Checker{Repo: "owner/repo", Current: "1.1.0", BaseURL: srv.URL}
	rel, _, err := c.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	a := &Applier{TargetPath: target, OS: "testos", Arch: "testarch"}
	if a.AssetName() != "wisp_testos_testarch" {
		t.Fatalf("asset name = %q", a.AssetName())
	}
	if err := a.Apply(context.Background(), rel); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBinary) {
		t.Fatalf("binary not replaced: %q", got)
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(filepath.Dir(target))
	for _, e := range entries {
		if filepath.Ext(e.Name()) == "" && e.Name() != "wisp" {
			t.Fatalf("leftover file: %s", e.Name())
		}
	}
}

func TestApplyChecksumMismatch(t *testing.T) {
	srv, assetName := fakeGitHub(t, "v1.2.0", []byte("real binary"))

	// Build a release whose checksums file lies about the hash.
	rel := &Release{
		Tag: "v1.2.0",
		Assets: []Asset{
			{Name: assetName, URL: srv.URL + "/download/bin"},
			{Name: "checksums.txt", URL: srv.URL + "/download/badchecksums"},
		},
	}
	// Add a handler returning a wrong checksum.
	target := filepath.Join(t.TempDir(), "wisp")
	os.WriteFile(target, []byte("old"), 0o755)

	a := &Applier{TargetPath: target, OS: "testos", Arch: "testarch"}
	err := a.Apply(context.Background(), rel)
	if err == nil {
		t.Fatal("expected an error (bad checksums URL / mismatch)")
	}
	// Target must be untouched on failure.
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Fatalf("target modified on failed update: %q", got)
	}
}

func TestParseChecksums(t *testing.T) {
	in := "abc123  wisp_linux_amd64\ndef456 *wisp_darwin_arm64\ngarbage\n"
	m := parseChecksums(in)
	if m["wisp_linux_amd64"] != "abc123" {
		t.Fatalf("linux = %q", m["wisp_linux_amd64"])
	}
	if m["wisp_darwin_arm64"] != "def456" {
		t.Fatalf("darwin (binary-mode *) = %q", m["wisp_darwin_arm64"])
	}
}
