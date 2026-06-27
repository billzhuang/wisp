// Package update implements GitHub Releases–based self-update for wisp, the
// click-to-install flow modelled on Ghostty's updater. There are two halves:
//
//   - Checker.CheckForUpdate queries the repo's latest release and reports
//     whether it is newer than the running build.
//   - Applier.Apply downloads the matching platform asset, verifies its SHA-256
//     against the release's checksums file, and atomically replaces the running
//     executable. The user restarts to run the new version.
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
	tmp, err := os.CreateTemp(dir, ".wisp-update-*")
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
	// Atomic replace. On Unix the running binary's inode stays valid, so this
	// is safe while wisp is executing; the new binary is used on next launch.
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("update: replacing %s: %w", target, err)
	}
	return nil
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
