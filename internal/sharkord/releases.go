package sharkord

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/httpx"
)

// Client talks to GitHub for Sharkord releases.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	// APIBase and DownloadBase can point at a test server.
	APIBase      string // https://api.github.com/repos/sharkord/sharkord
	DownloadBase string // https://github.com/sharkord/sharkord/releases/download
	// CacheFile holds the release list for CacheFor. Empty means no cache.
	CacheFile string
	CacheFor  time.Duration
	Now       func() time.Time
}

func NewClient(userAgent, cacheFile string) *Client {
	return &Client{
		HTTP:         &http.Client{Timeout: 10 * time.Minute},
		UserAgent:    userAgent,
		APIBase:      "https://api.github.com/repos/sharkord/sharkord",
		DownloadBase: "https://github.com/sharkord/sharkord/releases/download",
		CacheFile:    cacheFile,
		CacheFor:     time.Hour,
	}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Release is one published Sharkord release.
type Release struct {
	Version     string    `json:"version"`
	PublishedAt time.Time `json:"publishedAt"`
	Tested      bool      `json:"tested"`
}

type cachedReleases struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Releases  []Release `json:"releases"`
}

// Releases lists published releases, newest first, skipping drafts and
// pre-releases. Answers are cached, since unauthenticated calls are limited
// to 60 an hour.
func (c *Client) Releases(ctx context.Context) ([]Release, error) {
	if c.CacheFile != "" {
		var cached cachedReleases
		if b, err := os.ReadFile(c.CacheFile); err == nil && json.Unmarshal(b, &cached) == nil &&
			c.now().Sub(cached.FetchedAt) < c.CacheFor {
			return cached.Releases, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBase+"/releases?per_page=30", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := httpx.Do(ctx, c.HTTP, func() (*http.Request, error) { return req, nil })
	if err != nil {
		return nil, fmt.Errorf("list Sharkord releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list Sharkord releases: GitHub answered %s", resp.Status)
	}
	var raw []struct {
		TagName     string    `json:"tag_name"`
		Draft       bool      `json:"draft"`
		Prerelease  bool      `json:"prerelease"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("list Sharkord releases: %w", err)
	}
	var out []Release
	for _, r := range raw {
		v := strings.TrimPrefix(r.TagName, "v")
		if r.Draft || r.Prerelease || ValidVersion(v) != nil {
			continue
		}
		_, tested := FindTested(v)
		out = append(out, Release{Version: v, PublishedAt: r.PublishedAt, Tested: tested})
	}
	if c.CacheFile != "" {
		if b, err := json.Marshal(cachedReleases{FetchedAt: c.now(), Releases: out}); err == nil {
			_ = fsutil.WriteFileAtomic(c.CacheFile, b, 0o600, nil)
		}
	}
	return out, nil
}

// AssetName is the Linux build for a Go architecture.
func AssetName(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "sharkord-linux-x64", nil
	case "arm64":
		return "sharkord-linux-arm64", nil
	}
	return "", fmt.Errorf("Sharkord has no Linux build for %s", goarch)
}

// BinaryPath is where a version is installed.
func BinaryPath(optDir, version string) string {
	return filepath.Join(optDir, version, "sharkord")
}

type releaseManifest struct {
	Version   string `json:"version"`
	Artifacts []struct {
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Checksum string `json:"checksum"`
	} `json:"artifacts"`
}

// Install downloads version for this machine into optDir/<version>/sharkord,
// checking its size and SHA-256 against release.json from the same release.
// It does nothing if the binary is already there. The checksum catches a
// broken download, not a tampered release, since both files come from the
// same place.
func (c *Client) Install(ctx context.Context, optDir, version string) error {
	if err := ValidVersion(version); err != nil {
		return err
	}
	dst := BinaryPath(optDir, version)
	if fi, err := os.Stat(dst); err == nil && fi.Mode().IsRegular() {
		return nil
	}
	asset, err := AssetName(runtime.GOARCH)
	if err != nil {
		return err
	}
	base := c.DownloadBase + "/v" + version + "/"

	var manifest releaseManifest
	body, err := c.get(ctx, base+"release.json", 1<<20)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		return fmt.Errorf("read release.json: %w", err)
	}
	var size int64
	var sum string
	for _, a := range manifest.Artifacts {
		if a.Name == asset {
			size, sum = a.Size, strings.ToLower(a.Checksum)
		}
	}
	if sum == "" {
		return fmt.Errorf("release.json for %s doesn't list %s", version, asset)
	}

	dir := filepath.Dir(dst)
	if err := fsutil.MkdirAllMode(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".sharkord-download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+asset, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := httpx.Do(ctx, c.HTTP, func() (*http.Request, error) { return req, nil })
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", asset, resp.Status)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, size+1))
	if err != nil {
		return fmt.Errorf("download %s: %w", asset, err)
	}
	if n != size {
		return fmt.Errorf("download %s: got %d bytes, release.json says %d", asset, n, size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("download %s: checksum %s doesn't match release.json (%s)", asset, got, sum)
	}
	if err := tmp.Chmod(0o755); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func (c *Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := httpx.Do(ctx, c.HTTP, func() (*http.Request, error) { return req, nil })
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if int64(len(b)) > limit {
		return nil, errors.New("GET " + url + ": answer too large")
	}
	return b, err
}
