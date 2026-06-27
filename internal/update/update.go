// Package update implements GitHub Releases–based self-update for wisp, the
// click-to-install flow modelled on Ghostty's updater. There are two halves:
//
//   - Checker.CheckForUpdate queries the repo's latest release and reports
//     whether it is newer than the running build.
//   - Applier.Apply downloads the matching platform asset, verifies its SHA-256
//     against the release's checksums file, and atomically replaces the running
//     executable. The user restarts to run the new version.
//
// Applier.CleanupLeftovers complements Apply: run once at startup, it reclaims
// the previous binary and any interrupted-download temp files so successive
// updates don't accumulate disk.
//
// Both halves take their HTTP client and endpoints as fields so the whole flow
// is exercised against an httptest server in tests — no real network or GitHub
// account needed.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Release is the subset of a GitHub release wisp cares about.
type Release struct {
	Tag     string  `json:"tag_name"`
	Name    string  `json:"name"`
	Notes   string  `json:"body"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// Version returns the release tag without a leading "v".
func (r *Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

// Asset is a downloadable release artifact.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

const (
	// tempPattern is the os.CreateTemp pattern for the in-progress download. The
	// trailing "*" is where CreateTemp inserts random characters, and it doubles
	// as the glob CleanupLeftovers uses to reclaim interrupted downloads.
	tempPattern = ".wisp-update-*"
	// backupSuffix names the previous binary moved aside during the atomic swap.
	backupSuffix = ".old"
	// tempStaleAfter is how old a temp file must be before CleanupLeftovers
	// reclaims it. A live download is recent; only files left by an interrupted
	// one are this old. The grace window keeps a concurrent in-flight update
	// (its temp file matches the same glob) from being swept out from under it.
	tempStaleAfter = time.Hour
)

// Checker queries GitHub for the latest release.
type Checker struct {
	// Repo is "owner/name". Required.
	Repo string
	// Current is the running version; releases newer than this are updates.
	Current string
	// HTTPClient defaults to a client with a sane timeout.
	HTTPClient *http.Client
	// BaseURL is the GitHub API base; defaults to https://api.github.com.
	// Overridable for tests.
	BaseURL string
}

func (c *Checker) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (c *Checker) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return "https://api.github.com"
}

// Latest fetches the latest published release.
func (c *Checker) Latest(ctx context.Context) (*Release, error) {
	if c.Repo == "" {
		return nil, fmt.Errorf("update: repo is required")
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", c.baseURL(), c.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("update: GitHub returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("update: decoding release: %w", err)
	}
	return &rel, nil
}

// CheckForUpdate returns the latest release and whether it is newer than the
// running version. When the bool is false the Release may still be returned for
// display ("you are on the latest version").
func (c *Checker) CheckForUpdate(ctx context.Context) (*Release, bool, error) {
	rel, err := c.Latest(ctx)
	if err != nil {
		return nil, false, err
	}
	return rel, IsNewer(c.Current, rel.Version()), nil
}

// Applier downloads and installs a release.
type Applier struct {
	// HTTPClient defaults to a client with a longer timeout (binaries are big).
	HTTPClient *http.Client
	// TargetPath is the executable to replace; defaults to os.Executable().
	TargetPath string
	// OS, Arch select the asset; default to runtime.GOOS/GOARCH.
	OS, Arch string
	// Prefix is the asset-name prefix identifying the build flavor, e.g.
	// "wisp" for the CLI build or "wisp-gui" for the Ebitengine build, so each
	// flavor self-updates to the matching artifact. Defaults to "wisp".
	Prefix string
}

func (a *Applier) client() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return &http.Client{Timeout: 5 * time.Minute}
}

func (a *Applier) goos() string {
	if a.OS != "" {
		return a.OS
	}
	return runtime.GOOS
}

func (a *Applier) goarch() string {
	if a.Arch != "" {
		return a.Arch
	}
	return runtime.GOARCH
}

func (a *Applier) prefix() string {
	if a.Prefix != "" {
		return a.Prefix
	}
	return "wisp"
}

// AssetName is the per-platform binary asset name the CD pipeline publishes,
// e.g. "wisp_linux_amd64" or "wisp-gui_darwin_arm64" (with ".exe" on Windows).
func (a *Applier) AssetName() string {
	name := fmt.Sprintf("%s_%s_%s", a.prefix(), a.goos(), a.goarch())
	if a.goos() == "windows" {
		name += ".exe"
	}
	return name
}

func (a *Applier) targetPath() (string, error) {
	if a.TargetPath != "" {
		return a.TargetPath, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// Apply downloads the asset for this platform from rel, verifies its SHA-256
// against the release's checksums file, and atomically replaces the target
// executable. The caller restarts the process to run the new build.
func (a *Applier) Apply(ctx context.Context, rel *Release) error {
	bin := findAsset(rel.Assets, a.AssetName())
	if bin == nil {
		return fmt.Errorf("update: release %s has no asset %q", rel.Version(), a.AssetName())
	}
	sums := findChecksums(rel.Assets)
	if sums == nil {
		return fmt.Errorf("update: release %s has no checksums file", rel.Version())
	}

	target, err := a.targetPath()
	if err != nil {
		return fmt.Errorf("update: locating target: %w", err)
	}

	want, err := a.fetchChecksum(ctx, sums.URL, bin.Name)
	if err != nil {
		return err
	}

	// Download the new binary to a temp file in the target directory so the
	// final rename is atomic (same filesystem).
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return fmt.Errorf("update: creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once renamed

	sum, err := a.download(ctx, bin.URL, tmp)
	tmp.Close()
	if err != nil {
		return err
	}
	if sum != want {
		return fmt.Errorf("update: checksum mismatch for %s (got %s, want %s)", bin.Name, sum, want)
	}

	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("update: chmod: %w", err)
	}
	// Replace the running executable. Renaming the current binary aside first
	// (rather than overwriting it directly) is the portable pattern: on Windows
	// the live executable is locked and cannot be overwritten, but it *can* be
	// renamed. On Unix this is equivalent to an atomic swap. The .old file is
	// removed best-effort below; when that fails (Windows keeps the still-running
	// old binary locked) CleanupLeftovers reclaims it on the next launch.
	backup := target + backupSuffix
	_ = os.Remove(backup) // clear any stale backup
	if err := os.Rename(target, backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("update: moving current binary aside: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		// Roll back so the user is left with a working binary.
		_ = os.Rename(backup, target)
		return fmt.Errorf("update: replacing %s: %w", target, err)
	}
	_ = os.Remove(backup) // best-effort; on Windows may be locked until restart
	return nil
}

// CleanupLeftovers reclaims update artifacts left next to the target executable
// so repeated updates don't accumulate disk. It removes two kinds of file:
//
//   - "<target>.old": the previous binary moved aside during the atomic swap.
//     On Unix Apply removes it immediately, but on Windows the still-running old
//     process keeps it locked, so it can only be deleted once that process has
//     exited — i.e. on the next launch, which is what this does.
//   - ".wisp-update-*" older than tempStaleAfter: a partially downloaded binary
//     from an update that was interrupted (crash, power loss, SIGKILL) before the
//     atomic rename, whose deferred cleanup therefore never ran. Recent temp
//     files are left alone so a concurrent in-flight download isn't deleted out
//     from under the process writing it.
//
// It is best-effort: it never returns an error and ignores files it cannot
// remove, so a launch is never blocked by cleanup. It returns the number of
// files removed (handy for logging and tests). Call it once at startup.
func (a *Applier) CleanupLeftovers() int {
	target, err := a.targetPath()
	if err != nil {
		return 0
	}
	removed := 0
	if err := os.Remove(target + backupSuffix); err == nil {
		removed++
	}
	// Reap orphaned temp files in the executable's own directory. We scan with
	// os.ReadDir + a literal prefix match rather than filepath.Glob: an install
	// path containing glob metacharacters (e.g. "/opt/app[1]/") would make Glob
	// misread the directory itself and match nothing. Info() also reuses the
	// dirent, sparing a redundant os.Stat per entry.
	dir := filepath.Dir(target)
	tempPrefix := strings.TrimSuffix(tempPattern, "*")
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), tempPrefix) {
			continue
		}
		// Skip files young enough to be a download still in progress (possibly by
		// a concurrent process); they'll be reaped on a later launch if orphaned.
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < tempStaleAfter {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			removed++
		}
	}
	return removed
}

func (a *Applier) download(ctx context.Context, url string, w io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: downloading %s: %s", url, resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), resp.Body); err != nil {
		return "", fmt.Errorf("update: downloading %s: %w", url, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fetchChecksum downloads the checksums file and returns the expected hex
// SHA-256 for assetName. The file format is the standard `sha256sum` output:
//
//	<hex>  <filename>
func (a *Applier) fetchChecksum(ctx context.Context, url, assetName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("update: downloading checksums: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	sum := parseChecksums(string(body))[assetName]
	if sum == "" {
		return "", fmt.Errorf("update: checksums file has no entry for %s", assetName)
	}
	return sum, nil
}

func parseChecksums(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// The filename may be prefixed with "*" in binary mode.
		name := strings.TrimPrefix(fields[1], "*")
		out[filepath.Base(name)] = strings.ToLower(fields[0])
	}
	return out
}

func findAsset(assets []Asset, name string) *Asset {
	for i := range assets {
		if assets[i].Name == name {
			return &assets[i]
		}
	}
	return nil
}

func findChecksums(assets []Asset) *Asset {
	for i := range assets {
		n := strings.ToLower(assets[i].Name)
		if strings.Contains(n, "checksum") || strings.HasSuffix(n, ".sha256") {
			return &assets[i]
		}
	}
	return nil
}
