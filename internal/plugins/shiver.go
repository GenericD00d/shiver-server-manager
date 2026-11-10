// Package plugins fetches, checks and unpacks the Shiver plugin, the server
// half of Shiver (https://github.com/GenericD00d/Shiver).
package plugins

import (
	"bufio"
	"bytes"
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
	"regexp"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/httpx"
)

// ID is Shiver's plugin ID.
const ID = "shiver"

// MaxZip is the largest plugin zip accepted (Sharkord's own limit).
const MaxZip = 20 << 20

// Manifest is a plugin's manifest.json.
type Manifest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Author      string `json:"author"`
	Description string `json:"description"`
	Version     string `json:"version"`
	SDKVersion  int    `json:"sdkVersion"`
}

var versionRE = regexp.MustCompile(`^\d+\.\d+\.\d+(-[a-zA-Z0-9-.]+)?$`)

// Check verifies a manifest is Shiver's and speaks sdk.
func (m Manifest) Check(sdk int) error {
	if m.ID != ID {
		return fmt.Errorf("the plugin's id is %q, not %q", m.ID, ID)
	}
	return m.Valid(sdk)
}

// idRE: a plugin ID is also its folder's name under plugins/.
var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Valid verifies any plugin's manifest: a safe ID, a version, and the SDK
// the server's Sharkord speaks.
func (m Manifest) Valid(sdk int) error {
	switch {
	case !idRE.MatchString(m.ID) || strings.Contains(m.ID, ".."):
		return fmt.Errorf("the plugin's id %q isn't valid (lowercase letters, digits, dots, dashes and underscores)", m.ID)
	case !versionRE.MatchString(m.Version):
		return fmt.Errorf("the plugin's version %q isn't valid", m.Version)
	case m.SDKVersion != sdk:
		return fmt.Errorf("the plugin %s needs plugin SDK %d, but the server's Sharkord speaks SDK %d", m.ID, m.SDKVersion, sdk)
	}
	return nil
}

// Release is the latest Shiver release's plugin.
type Release struct {
	Tag       string    `json:"tag"`
	ZipName   string    `json:"zipName"`
	ZipURL    string    `json:"zipURL"`
	SumsURL   string    `json:"sumsURL"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// PluginVersion reads the plugin's version from its zip name
// (shiver-plugin-<version>.zip); the release tag is the apps' version.
func (r Release) PluginVersion() string {
	return strings.TrimSuffix(strings.TrimPrefix(r.ZipName, "shiver-plugin-"), ".zip")
}

type Client struct {
	HTTP      *http.Client
	UserAgent string
	APIBase   string // https://api.github.com/repos/GenericD00d/Shiver
	CacheFile string
	CacheFor  time.Duration
}

func NewClient(userAgent, cacheFile string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 2 * time.Minute}, UserAgent: userAgent,
		APIBase: "https://api.github.com/repos/GenericD00d/Shiver", CacheFile: cacheFile, CacheFor: time.Hour}
}

var zipName = regexp.MustCompile(`^shiver-plugin-\d+\.\d+\.\d+(-[a-zA-Z0-9-.]+)?\.zip$`)

// Latest finds the latest release's plugin zip and checksums (cached).
func (c *Client) Latest(ctx context.Context) (Release, error) {
	var r Release
	if c.CacheFile != "" {
		if b, err := os.ReadFile(c.CacheFile); err == nil && json.Unmarshal(b, &r) == nil && time.Since(r.FetchedAt) < c.CacheFor {
			return r, nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIBase+"/releases/latest", nil)
	if err != nil {
		return r, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := httpx.Do(ctx, c.HTTP, func() (*http.Request, error) { return req, nil })
	if err != nil {
		return r, fmt.Errorf("look up the latest Shiver release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return r, fmt.Errorf("look up the latest Shiver release: GitHub answered %s", resp.Status)
	}
	var raw struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return r, err
	}
	r = Release{Tag: raw.TagName, FetchedAt: time.Now()}
	for _, a := range raw.Assets {
		switch {
		case zipName.MatchString(a.Name):
			r.ZipName, r.ZipURL = a.Name, a.URL
		case a.Name == "SHA256SUMS.txt":
			r.SumsURL = a.URL
		}
	}
	if r.ZipURL == "" || r.SumsURL == "" {
		return r, fmt.Errorf("Shiver %s has no plugin zip or SHA256SUMS.txt", r.Tag)
	}
	if c.CacheFile != "" {
		if b, err := json.Marshal(r); err == nil {
			_ = fsutil.WriteFileAtomic(c.CacheFile, b, 0o600, nil)
		}
	}
	return r, nil
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
		return nil, fmt.Errorf("GET %s: too large", url)
	}
	return b, err
}

// SumFor finds a file's SHA-256 in a SHA256SUMS file ("<hex> *<name>" or
// "<hex>  <name>").
func SumFor(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS.txt has no line for %s", name)
}

// Download fetches the zip and checks it against SHA256SUMS.txt from the
// same release. That catches a broken download, not a tampered release.
func (c *Client) Download(ctx context.Context, r Release) ([]byte, error) {
	sums, err := c.get(ctx, r.SumsURL, 1<<20)
	if err != nil {
		return nil, err
	}
	want, err := SumFor(sums, r.ZipName)
	if err != nil {
		return nil, err
	}
	zb, err := c.get(ctx, r.ZipURL, MaxZip)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(zb)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s's checksum doesn't match SHA256SUMS.txt", r.ZipName)
	}
	return zb, nil
}

func hasStructure(dir string) bool {
	for _, f := range []string{"manifest.json", "server/index.js", "client/index.js"} {
		if fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(f))); err != nil || !fi.Mode().IsRegular() {
			return false
		}
	}
	return true
}

func pluginRoot(dir string) (string, error) {
	if hasStructure(dir) {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() && hasStructure(filepath.Join(dir, e.Name())) {
			found = append(found, filepath.Join(dir, e.Name()))
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", errors.New("the archive doesn't hold a plugin (manifest.json, server/index.js and client/index.js)")
	}
	return "", errors.New("the archive holds more than one plugin")
}

// Installed reads an instance's installed Shiver manifest, if any.
func Installed(dataPath string) (Manifest, bool) { return InstalledPlugin(dataPath, ID) }

// InstalledPlugin reads an installed plugin's manifest, if any.
func InstalledPlugin(dataPath, id string) (Manifest, bool) {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dataPath, "plugins", id, "manifest.json"))
	if err != nil || json.Unmarshal(b, &m) != nil {
		return m, false
	}
	return m, true
}
