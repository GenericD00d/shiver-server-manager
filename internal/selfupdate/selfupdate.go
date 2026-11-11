// Package selfupdate finds the manager's latest release on GitHub and
// downloads its package, checked against the release's SHA256SUMS.
//
// Installing runs apt, dnf or zypper in a unit of its own (systemd-run):
// the package's install script restarts the web service, which would
// otherwise take an update started from the web page down with it, halfway
// through the install.
package selfupdate

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
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
)

// Client talks to the manager's GitHub repository.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	APIBase   string // https://api.github.com/repos/GenericD00d/shiver-server-manager
	// TokenFile holds a GitHub token, needed while the repository is
	// private. Missing is fine.
	TokenFile string
	// CacheFile keeps the latest release for CacheFor (the health check
	// asks every 5 minutes; GitHub allows 60 unauthenticated calls an hour).
	CacheFile string
	CacheFor  time.Duration
	Now       func() time.Time
}

func NewClient(userAgent, tokenFile, cacheFile string) *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 10 * time.Minute},
		UserAgent: userAgent,
		APIBase:   "https://api.github.com/repos/GenericD00d/shiver-server-manager",
		TokenFile: tokenFile,
		CacheFile: cacheFile,
		CacheFor:  6 * time.Hour,
	}
}

// Asset is one file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"` // the API URL, which also works with a token
}

// Release is the latest published release.
type Release struct {
	Version   string    `json:"version"`
	FetchedAt time.Time `json:"fetchedAt"`
	Assets    []Asset   `json:"assets"`
}

var versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// ValidVersion: plain x.y.z, safe in file names.
func ValidVersion(v string) bool { return versionRe.MatchString(v) }

// Newer reports whether latest is newer than current. A development build
// ("dev", or anything that isn't x.y.z) is never offered updates.
func Newer(latest, current string) bool {
	return ValidVersion(latest) && ValidVersion(current) && sharkord.CompareVersions(latest, current) > 0
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) token() string {
	if c.TokenFile == "" {
		return ""
	}
	b, err := os.ReadFile(c.TokenFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (c *Client) request(ctx context.Context, url, accept string) (*http.Response, error) {
	resp, err := httpx.Do(ctx, c.HTTP, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", accept)
		req.Header.Set("User-Agent", c.UserAgent)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		if t := c.token(); t != "" {
			// Go drops this header when GitHub redirects the download to
			// its storage host.
			req.Header.Set("Authorization", "Bearer "+t)
		}
		return req, nil
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		switch {
		case resp.StatusCode == http.StatusNotFound && c.token() == "":
			return nil, errors.New("GitHub answered 404: the repository is private, so the manager needs a token to see its releases (self-update token)")
		case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized:
			return nil, fmt.Errorf("GitHub answered %s: check the saved token can read the repository's contents (self-update token)", resp.Status)
		}
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	return resp, nil
}

// Latest returns the latest release. fresh skips the cache.
func (c *Client) Latest(ctx context.Context, fresh bool) (Release, error) {
	if !fresh && c.CacheFile != "" {
		var r Release
		if b, err := os.ReadFile(c.CacheFile); err == nil && json.Unmarshal(b, &r) == nil && c.now().Sub(r.FetchedAt) < c.CacheFor {
			return r, nil
		}
	}
	resp, err := c.request(ctx, c.APIBase+"/releases/latest", "application/vnd.github+json")
	if err != nil {
		return Release{}, fmt.Errorf("look up the manager's latest release: %w", err)
	}
	defer resp.Body.Close()
	var raw struct {
		TagName string  `json:"tag_name"`
		Assets  []Asset `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&raw); err != nil {
		return Release{}, fmt.Errorf("look up the manager's latest release: %w", err)
	}
	r := Release{Version: strings.TrimPrefix(raw.TagName, "v"), FetchedAt: c.now(), Assets: raw.Assets}
	if !ValidVersion(r.Version) {
		return Release{}, fmt.Errorf("the latest release's tag %q isn't a version", raw.TagName)
	}
	if c.CacheFile != "" {
		if b, err := json.Marshal(r); err == nil {
			_ = fsutil.WriteFileAtomic(c.CacheFile, b, 0o600, nil)
		}
	}
	return r, nil
}

// PackageName is the release's package for a Go architecture in a format
// ("deb" or "rpm"), named as the release workflow names it (nfpm's
// defaults).
func PackageName(cmd, version, goarch, format string) string {
	if format == "rpm" {
		arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
		return fmt.Sprintf("%s-%s-1.%s.rpm", cmd, version, arch)
	}
	return fmt.Sprintf("%s_%s_%s.deb", cmd, version, goarch)
}

func (r Release) asset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// Download saves the package into dir and checks it against SHA256SUMS. The
// checksum catches a broken download; HTTPS to GitHub is what vouches for
// the release itself.
func (c *Client) Download(ctx context.Context, r Release, name, dir string) (string, error) {
	sumsAsset, ok := r.asset("SHA256SUMS")
	if !ok {
		return "", errors.New("the release has no SHA256SUMS")
	}
	pkg, ok := r.asset(name)
	if !ok {
		return "", fmt.Errorf("the release has no %s", name)
	}
	resp, err := c.request(ctx, sumsAsset.URL, "application/octet-stream")
	if err != nil {
		return "", fmt.Errorf("download SHA256SUMS: %w", err)
	}
	sums, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if err != nil {
		return "", fmt.Errorf("download SHA256SUMS: %w", err)
	}
	want, err := sumFor(sums, name)
	if err != nil {
		return "", err
	}

	if err := fsutil.MkdirAllMode(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	resp, err = c.request(ctx, pkg.URL, "application/octet-stream")
	if err != nil {
		return "", fmt.Errorf("download %s: %w", name, err)
	}
	defer resp.Body.Close()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, 512<<20)); err != nil {
		return "", fmt.Errorf("download %s: %w", name, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("%s doesn't match SHA256SUMS (got %s, want %s)", name, got, want)
	}
	if err := tmp.Chmod(0o644); err != nil { // apt reads it as _apt
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	dst := filepath.Join(dir, name)
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", err
	}
	return dst, nil
}

func sumFor(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			if _, err := hex.DecodeString(f[0]); err != nil || len(f[0]) != 64 {
				return "", fmt.Errorf("SHA256SUMS has a bad line for %s", name)
			}
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("SHA256SUMS doesn't list %s", name)
}

// InstallArgs is the systemd-run command that installs a downloaded
// package in its own unit with a package manager ("apt", "dnf" or
// "zypper"). wait keeps the terminal attached to it. Changed config files
// are kept (dpkg's confold; rpm's noreplace files do that already).
func InstallArgs(unit, pkg, manager string, wait bool) []string {
	args := []string{"systemd-run", "--unit=" + unit, "--collect", "--quiet",
		"--setenv=DEBIAN_FRONTEND=noninteractive"}
	if wait {
		args = append(args, "--wait", "--pipe")
	}
	switch manager {
	case "dnf":
		return append(args, "/usr/bin/dnf", "install", "-y", pkg)
	case "zypper":
		// The release's packages aren't signed; SHA256SUMS over HTTPS
		// vouches for them instead.
		return append(args, "/usr/bin/zypper", "--non-interactive", "install", "--allow-unsigned-rpm", pkg)
	}
	return append(args, "/usr/bin/apt-get", "install", "-y",
		"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", pkg)
}
