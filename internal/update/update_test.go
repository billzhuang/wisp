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
	"time"
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
	// A well-formed checksums file whose hash is deliberately wrong, to exercise
	// the verification-mismatch branch (not just a download failure).
	mux.HandleFunc("/download/badchecksums", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", "00000000000000000000000000000000000000000000000000000000deadbeef", assetName)
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

func TestAssetNameFlavorAndOS(t *testing.T) {
	cases := []struct {
		prefix, os, arch, want string
	}{
		{"", "linux", "amd64", "wisp_linux_amd64"},
		{"wisp", "darwin", "arm64", "wisp_darwin_arm64"},
		{"wisp-gui", "darwin", "arm64", "wisp-gui_darwin_arm64"},
		{"wisp", "windows", "amd64", "wisp_windows_amd64.exe"},
		{"wisp-gui", "windows", "arm64", "wisp-gui_windows_arm64.exe"},
	}
	for _, c := range cases {
		a := &Applier{Prefix: c.prefix, OS: c.os, Arch: c.arch}
		if got := a.AssetName(); got != c.want {
			t.Errorf("AssetName(prefix=%q,%s/%s) = %q, want %q", c.prefix, c.os, c.arch, got, c.want)
		}
	}
}

func TestApplyMissingAssetForPlatform(t *testing.T) {
	srv, _ := fakeGitHub(t, "v1.2.0", []byte("bin"))
	c := &Checker{Repo: "owner/repo", Current: "1.0.0", BaseURL: srv.URL}
	rel, _, err := c.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The fake release only has wisp_testos_testarch; ask for a different arch.
	a := &Applier{OS: "otheros", Arch: "otherarch"}
	err = a.Apply(context.Background(), rel)
	if err == nil {
		t.Fatal("expected error for missing platform asset")
	}
}

func TestApplyMissingChecksums(t *testing.T) {
	target := filepath.Join(t.TempDir(), "wisp")
	os.WriteFile(target, []byte("old"), 0o755)
	rel := &Release{
		Tag:    "v1.0.0",
		Assets: []Asset{{Name: "wisp_testos_testarch", URL: "http://example.invalid/bin"}},
	}
	a := &Applier{TargetPath: target, OS: "testos", Arch: "testarch"}
	if err := a.Apply(context.Background(), rel); err == nil {
		t.Fatal("expected error when no checksums asset is present")
	}
}

func TestCheckForUpdatePropagatesError(t *testing.T) {
	c := &Checker{Repo: "owner/repo", Current: "1.0.0", BaseURL: "http://127.0.0.1:0"}
	if _, _, err := c.CheckForUpdate(context.Background()); err == nil {
		t.Fatal("expected error from unreachable base URL")
	}
}

func TestLatestRequiresRepo(t *testing.T) {
	c := &Checker{Current: "1.0.0"}
	if _, err := c.Latest(context.Background()); err == nil {
		t.Fatal("expected error when repo is empty")
	}
}

func TestFindChecksumsVariants(t *testing.T) {
	assets := []Asset{
		{Name: "wisp_linux_amd64"},
		{Name: "wisp_1.0.0_checksums.txt"},
	}
	if findChecksums(assets) == nil {
		t.Fatal("should find *_checksums.txt")
	}
	assets[1].Name = "wisp.sha256"
	if findChecksums(assets) == nil {
		t.Fatal("should find *.sha256")
	}
	if findChecksums([]Asset{{Name: "wisp_linux_amd64"}}) != nil {
		t.Fatal("should not find a checksums asset when none present")
	}
}

func TestCleanupLeftovers(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "wisp")
	mustWrite(t, target, "current binary")
	mustWrite(t, target+".old", "previous binary") // Windows-style leftover
	// Orphaned downloads: old enough to be past the in-flight grace window.
	staleA := filepath.Join(dir, ".wisp-update-abc")
	staleB := filepath.Join(dir, ".wisp-update-xyz")
	mustWriteOld(t, staleA, "x")
	mustWriteOld(t, staleB, "y")
	// Files that must survive: the live binary, anything not ours, and a temp
	// file recent enough to be a download still in progress.
	mustWrite(t, filepath.Join(dir, "notes.txt"), "keep me")
	mustWrite(t, filepath.Join(dir, "wisp.cfg"), "keep me too")
	fresh := filepath.Join(dir, ".wisp-update-inflight")
	mustWrite(t, fresh, "downloading")

	a := &Applier{TargetPath: target}
	if got := a.CleanupLeftovers(); got != 3 {
		t.Fatalf("removed %d files, want 3 (.old + 2 stale temp)", got)
	}

	mustExist(t, target)
	mustExist(t, filepath.Join(dir, "notes.txt"))
	mustExist(t, filepath.Join(dir, "wisp.cfg"))
	mustExist(t, fresh) // in-flight download must not be swept
	mustGone(t, target+".old")
	mustGone(t, staleA)
	mustGone(t, staleB)
}

// TestCleanupLeftoversGlobMetacharDir guards the os.ReadDir approach: an install
// directory whose name contains glob metacharacters must not defeat cleanup.
// filepath.Glob would read "[1]" as a character class and match nothing here.
func TestCleanupLeftoversGlobMetacharDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app[1]")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "wisp")
	mustWrite(t, target, "current binary")
	mustWrite(t, target+".old", "previous binary")
	stale := filepath.Join(dir, ".wisp-update-orphan")
	mustWriteOld(t, stale, "x")

	a := &Applier{TargetPath: target}
	if got := a.CleanupLeftovers(); got != 2 {
		t.Fatalf("removed %d files, want 2 (.old + 1 stale temp) in a bracketed dir", got)
	}
	mustExist(t, target)
	mustGone(t, target+".old")
	mustGone(t, stale)
}

func TestCleanupLeftoversNothingToDo(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "wisp")
	mustWrite(t, target, "current binary")

	a := &Applier{TargetPath: target}
	if got := a.CleanupLeftovers(); got != 0 {
		t.Fatalf("removed %d files, want 0 on a clean directory", got)
	}
	mustExist(t, target)
}

// TestApplyLeavesNoLeftovers is the end-to-end disk-hygiene guarantee: after a
// successful update, a follow-up cleanup finds nothing to remove (Apply already
// reclaimed the .old on Unix), so disk usage does not grow per update.
func TestApplyLeavesNoLeftovers(t *testing.T) {
	srv, _ := fakeGitHub(t, "v1.2.0", []byte("new"))
	target := filepath.Join(t.TempDir(), "wisp")
	mustWrite(t, target, "old")

	c := &Checker{Repo: "owner/repo", Current: "1.1.0", BaseURL: srv.URL}
	rel, _, err := c.CheckForUpdate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a := &Applier{TargetPath: target, OS: "testos", Arch: "testarch"}
	if err := a.Apply(context.Background(), rel); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := a.CleanupLeftovers(); got != 0 {
		t.Fatalf("post-update cleanup removed %d files, want 0", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustWriteOld writes a file and backdates it well past tempStaleAfter so
// CleanupLeftovers treats it as an orphaned (not in-flight) download.
func mustWriteOld(t *testing.T, path, content string) {
	t.Helper()
	mustWrite(t, path, content)
	old := time.Now().Add(-2 * tempStaleAfter)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

func mustExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %s to exist: %v", filepath.Base(path), err)
	}
}

func mustGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be removed (err=%v)", filepath.Base(path), err)
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
