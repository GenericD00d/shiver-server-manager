package plugins

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
	"regexp"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/httpx"
)

// CatalogURL is plugins.json at the top of the manager's own repository:
// the Shiver plugins the manager offers. The contents API (rather than a
// raw link) works with a token while the repository is private.
const CatalogURL = "https://api.github.com/repos/GenericD00d/shiver-server-manager/contents/plugins.json?ref=main"

// CatalogEntry is one plugin in plugins.json.
type CatalogEntry struct {
	// ID is the plugin's id in its manifest (and its folder's name).
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Repo is the GitHub repository whose latest release has the plugin.
	Repo string `json:"repo"`
	// Asset matches the plugin archive's name; its one group is the
	// plugin's version.
	Asset string `json:"asset"`
	// Checksums names the release asset holding the archive's SHA-256, with
	// {version} filled in: a SHA256SUMS-style file, or a .json file with a
	// "checksum" field.
	Checksums string `json:"checksums"`
}

// Catalog is plugins.json.
type Catalog struct {
	Plugins   []CatalogEntry `json:"plugins"`
	FetchedAt time.Time      `json:"fetchedAt,omitempty"`
}

var repoRE = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Check makes sure every entry can be used safely.
func (c Catalog) Check() error {
	if len(c.Plugins) == 0 {
		return errors.New("the plugin list is empty")
	}
	seen := map[string]bool{}
	for _, e := range c.Plugins {
		switch {
		case !idRE.MatchString(e.ID) || strings.Contains(e.ID, ".."):
			return fmt.Errorf("the plugin list has a bad id %q", e.ID)
		case seen[e.ID]:
			return fmt.Errorf("the plugin list has %q twice", e.ID)
		case !repoRE.MatchString(e.Repo) || strings.Contains(e.Repo, ".."):
			return fmt.Errorf("the plugin list has a bad repository %q for %s", e.Repo, e.ID)
		case e.Checksums == "" || strings.ContainsAny(e.Checksums, "/\\"):
			return fmt.Errorf("the plugin list has a bad checksums file for %s", e.ID)
		}
		re, err := regexp.Compile(e.Asset)
		if err != nil || re.NumSubexp() != 1 {
			return fmt.Errorf("the plugin list's asset pattern for %s needs one group, for the version", e.ID)
		}
		seen[e.ID] = true
	}
	return nil
}

// Find returns the entry for a plugin id.
func (c Catalog) Find(id string) (CatalogEntry, bool) {
	for _, e := range c.Plugins {
		if e.ID == id {
			return e, true
		}
	}
	return CatalogEntry{}, false
}

// CatalogRelease is a catalog plugin's latest release.
type CatalogRelease struct {
	Tag       string    `json:"tag"`
	Version   string    `json:"version"`
	AssetName string    `json:"assetName"`
	AssetURL  string    `json:"assetURL"`
	SumsName  string    `json:"sumsName"`
	SumsURL   string    `json:"sumsURL"`
	FetchedAt time.Time `json:"fetchedAt"`
}

// CatalogClient reads plugins.json and the plugins' releases from GitHub,
// caching both for an hour.
type CatalogClient struct {
	HTTP      *http.Client
	UserAgent string
	URL       string // CatalogURL
	APIBase   string // https://api.github.com
	TokenFile string // a GitHub token, while the repository is private
	CacheFile string
	CacheFor  time.Duration
}

func NewCatalogClient(userAgent, tokenFile, cacheFile string) *CatalogClient {
	return &CatalogClient{HTTP: &http.Client{Timeout: 2 * time.Minute}, UserAgent: userAgent, URL: CatalogURL,
		APIBase: "https://api.github.com", TokenFile: tokenFile, CacheFile: cacheFile, CacheFor: time.Hour}
}

type catalogCache struct {
	Catalog  Catalog                   `json:"catalog"`
	Releases map[string]CatalogRelease `json:"releases"`
}

func (c *CatalogClient) loadCache() catalogCache {
	var cc catalogCache
	if c.CacheFile != "" {
		if b, err := os.ReadFile(c.CacheFile); err == nil {
			_ = json.Unmarshal(b, &cc)
		}
	}
	if cc.Releases == nil {
		cc.Releases = map[string]CatalogRelease{}
	}
	return cc
}

func (c *CatalogClient) saveCache(cc catalogCache) {
	if c.CacheFile == "" {
		return
	}
	if b, err := json.Marshal(cc); err == nil {
		_ = fsutil.WriteFileAtomic(c.CacheFile, b, 0o600, nil)
	}
}

func (c *CatalogClient) fresh(t time.Time) bool { return !t.IsZero() && time.Since(t) < c.CacheFor }

func (c *CatalogClient) token() string {
	if c.TokenFile == "" {
		return ""
	}
	b, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// get fetches url, with the token for GitHub's API.
func (c *CatalogClient) get(ctx context.Context, url, accept string, limit int64) ([]byte, int, error) {
	resp, err := httpx.Do(ctx, c.HTTP, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", c.UserAgent)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if t := c.token(); t != "" && strings.HasPrefix(url, c.APIBase+"/") {
			req.Header.Set("Authorization", "Bearer "+t)
		}
		return req, nil
	})
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if int64(len(b)) > limit {
		return nil, resp.StatusCode, errors.New("the file is too large")
	}
	return b, resp.StatusCode, err
}

// ErrUnavailable wraps every reason the plugin list can't be read.
var ErrUnavailable = errors.New("Shiver plugins are unavailable")

// Catalog reads plugins.json (cached).
func (c *CatalogClient) Catalog(ctx context.Context) (Catalog, error) {
	cc := c.loadCache()
	if c.fresh(cc.Catalog.FetchedAt) && cc.Catalog.Check() == nil {
		return cc.Catalog, nil
	}
	b, status, err := c.get(ctx, c.URL, "application/vnd.github.raw+json", 1<<20)
	if err != nil {
		hint := ""
		if status == http.StatusNotFound {
			hint = " (while the repository is private, save a GitHub token with System > Updates or 'self-update token')"
		}
		return Catalog{}, fmt.Errorf("%w: couldn't read the plugin list: %v%s", ErrUnavailable, err, hint)
	}
	var cat Catalog
	if err := json.Unmarshal(b, &cat); err != nil {
		return Catalog{}, fmt.Errorf("%w: the plugin list isn't valid JSON: %v", ErrUnavailable, err)
	}
	if err := cat.Check(); err != nil {
		return Catalog{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	cat.FetchedAt = time.Now()
	cc.Catalog = cat
	c.saveCache(cc)
	return cat, nil
}

// Latest finds a plugin's latest release (cached).
func (c *CatalogClient) Latest(ctx context.Context, e CatalogEntry) (CatalogRelease, error) {
	cc := c.loadCache()
	if r, ok := cc.Releases[e.ID]; ok && c.fresh(r.FetchedAt) {
		return r, nil
	}
	b, _, err := c.get(ctx, c.APIBase+"/repos/"+e.Repo+"/releases/latest", "application/vnd.github+json", 4<<20)
	if err != nil {
		return CatalogRelease{}, fmt.Errorf("look up %s's latest release: %w", e.Name, err)
	}
	var raw struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return CatalogRelease{}, fmt.Errorf("look up %s's latest release: %w", e.Name, err)
	}
	re := regexp.MustCompile(e.Asset) // checked by Catalog.Check
	r := CatalogRelease{Tag: raw.TagName, FetchedAt: time.Now()}
	for _, a := range raw.Assets {
		if m := re.FindStringSubmatch(a.Name); m != nil && versionRE.MatchString(m[1]) {
			r.AssetName, r.AssetURL, r.Version = a.Name, a.URL, m[1]
			break
		}
	}
	if r.AssetURL == "" {
		return r, fmt.Errorf("%s %s has no plugin archive", e.Name, r.Tag)
	}
	r.SumsName = strings.ReplaceAll(e.Checksums, "{version}", r.Version)
	for _, a := range raw.Assets {
		if a.Name == r.SumsName {
			r.SumsURL = a.URL
		}
	}
	if r.SumsURL == "" {
		return r, fmt.Errorf("%s %s has no %s", e.Name, r.Tag, r.SumsName)
	}
	cc.Releases[e.ID] = r
	c.saveCache(cc)
	return r, nil
}

// checksumIn reads the archive's SHA-256 from the checksums file: a
// "<version>.json" with a "checksum" field, or a SHA256SUMS-style list.
func checksumIn(sums []byte, sumsName, assetName string) (string, error) {
	if strings.HasSuffix(sumsName, ".json") {
		var j struct {
			Checksum string `json:"checksum"`
		}
		if err := json.Unmarshal(sums, &j); err != nil {
			return "", fmt.Errorf("%s isn't valid JSON: %w", sumsName, err)
		}
		if _, err := hex.DecodeString(j.Checksum); err != nil || len(j.Checksum) != 64 {
			return "", fmt.Errorf("%s has no SHA-256 checksum", sumsName)
		}
		return strings.ToLower(j.Checksum), nil
	}
	sum, err := SumFor(sums, assetName)
	if err != nil {
		return "", fmt.Errorf("%s has no line for %s", sumsName, assetName)
	}
	return sum, nil
}

// Download saves the release's archive to file and checks it against the
// release's checksum. That catches a broken download, not a tampered
// release.
func (c *CatalogClient) Download(ctx context.Context, r CatalogRelease, file string) error {
	sums, _, err := c.get(ctx, r.SumsURL, "", 1<<20)
	if err != nil {
		return fmt.Errorf("download %s: %w", r.SumsName, err)
	}
	want, err := checksumIn(sums, r.SumsName, r.AssetName)
	if err != nil {
		return err
	}
	b, _, err := c.get(ctx, r.AssetURL, "", MaxArchive)
	if err != nil {
		return fmt.Errorf("download %s: %w", r.AssetName, err)
	}
	got := sha256.Sum256(b)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s doesn't match its checksum in %s", r.AssetName, r.SumsName)
	}
	return os.WriteFile(file, b, 0o600)
}
