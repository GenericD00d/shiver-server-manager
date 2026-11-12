package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeGitHub serves a release with a package and SHA256SUMS. With a token
// set, it answers 404 to requests without it, as GitHub does for private
// repositories.
func fakeGitHub(t *testing.T, token string, pkg []byte, sums string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token != "" && r.Header.Get("Authorization") != "Bearer "+token {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Path {
		case "/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.2.0","assets":[{"name":"ssm_0.2.0_amd64.deb","url":"%[1]s/assets/1"},{"name":"SHA256SUMS","url":"%[1]s/assets/2"}]}`, srv.URL)
		case "/assets/1":
			if r.Header.Get("Accept") != "application/octet-stream" {
				t.Errorf("asset asked for %q", r.Header.Get("Accept"))
			}
			w.Write(pkg)
		case "/assets/2":
			w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func client(t *testing.T, srv *httptest.Server, token string) *Client {
	c := NewClient("test", filepath.Join(t.TempDir(), "token"), filepath.Join(t.TempDir(), "cache.json"))
	c.APIBase = srv.URL
	if token != "" {
		os.WriteFile(c.TokenFile, []byte(token+"\n"), 0o600)
	}
	return c
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestDownloadChecksTheSum(t *testing.T) {
	pkg := []byte("a debian package")
	srv := fakeGitHub(t, "", pkg, sum(pkg)+"  ssm_0.2.0_amd64.deb\n"+strings.Repeat("0", 64)+"  other.deb\n")
	c := client(t, srv, "")
	rel, err := c.Latest(context.Background(), true)
	if err != nil || rel.Version != "0.2.0" {
		t.Fatalf("%+v %v", rel, err)
	}
	dir := t.TempDir()
	path, err := c.Download(context.Background(), rel, "ssm_0.2.0_amd64.deb", dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(pkg) {
		t.Fatalf("got %q", b)
	}
	if _, err := c.Download(context.Background(), rel, "ssm_0.2.0_arm64.deb", dir); err == nil {
		t.Fatal("a package the release doesn't have")
	}
}

func TestDownloadRefusesAMismatch(t *testing.T) {
	srv := fakeGitHub(t, "", []byte("tampered"), sum([]byte("original"))+"  ssm_0.2.0_amd64.deb\n")
	c := client(t, srv, "")
	rel, _ := c.Latest(context.Background(), true)
	dir := t.TempDir()
	if _, err := c.Download(context.Background(), rel, "ssm_0.2.0_amd64.deb", dir); err == nil || !strings.Contains(err.Error(), "doesn't match") {
		t.Fatalf("%v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("left %v", entries)
	}
}

func TestPrivateRepositoryNeedsAToken(t *testing.T) {
	pkg := []byte("pkg")
	srv := fakeGitHub(t, "s3cret", pkg, sum(pkg)+"  ssm_0.2.0_amd64.deb\n")
	if _, err := client(t, srv, "").Latest(context.Background(), true); err == nil || !strings.Contains(err.Error(), "self-update token") {
		t.Fatalf("%v", err)
	}
	if _, err := client(t, srv, "wrong").Latest(context.Background(), true); err == nil || !strings.Contains(err.Error(), "check the saved token") {
		t.Fatalf("%v", err)
	}
	c := client(t, srv, "s3cret")
	rel, err := c.Latest(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Download(context.Background(), rel, "ssm_0.2.0_amd64.deb", t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestLatestIsCached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"tag_name":"v0.2.0","assets":[]}`)
	}))
	defer srv.Close()
	c := client(t, srv, "")
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return now }
	c.Latest(context.Background(), false)
	c.Latest(context.Background(), false)
	if calls != 1 {
		t.Fatalf("%d calls", calls)
	}
	c.Latest(context.Background(), true)
	now = now.Add(7 * time.Hour)
	c.Latest(context.Background(), false)
	if calls != 3 {
		t.Fatalf("%d calls", calls)
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"0.1.4", "0.1.3", true},
		{"0.1.10", "0.1.9", true},
		{"0.1.3", "0.1.3", false},
		{"0.1.2", "0.1.3", false},
		{"0.1.4", "dev", false},
		{"0.1.4", "0.1.3-5-gabc", false},
	} {
		if got := Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v", tc.latest, tc.current, got)
		}
	}
}

func TestPackageName(t *testing.T) {
	if got := PackageName("ssm", "0.2.0", "amd64", "deb"); got != "ssm_0.2.0_amd64.deb" {
		t.Error(got)
	}
	if got := PackageName("ssm", "0.2.0", "arm64", "rpm"); got != "ssm-0.2.0-1.aarch64.rpm" {
		t.Error(got)
	}
}

func TestInstallArgs(t *testing.T) {
	for manager, want := range map[string]string{
		"dnf":    "/usr/bin/dnf install -y /x.rpm",
		"zypper": "/usr/bin/zypper --non-interactive install --allow-unsigned-rpm /x.rpm",
	} {
		if got := strings.Join(InstallArgs("u", "/x.rpm", manager, true), " "); !strings.HasSuffix(got, "--wait --pipe "+want) {
			t.Errorf("%s: %s", manager, got)
		}
	}
	got := strings.Join(InstallArgs("u", "/x.deb", "apt", false), " ")
	if !strings.HasPrefix(got, "systemd-run --unit=u --collect") || strings.Contains(got, "--wait") || !strings.HasSuffix(got, "--force-confold /x.deb") {
		t.Fatal(got)
	}
	if !strings.Contains(strings.Join(InstallArgs("u", "/x.deb", "apt", true), " "), "--wait --pipe") {
		t.Fatal("no --wait")
	}
}
