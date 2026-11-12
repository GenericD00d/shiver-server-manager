package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The plugins.json at the top of the repository must load.
func TestRepositoryCatalog(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "plugins.json"))
	if err != nil {
		t.Fatal(err)
	}
	var c Catalog
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if err := c.Check(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"shiver", "temp-share", "shiver-media-player"} {
		if _, ok := c.Find(id); !ok {
			t.Errorf("%s isn't in plugins.json", id)
		}
	}
}

func TestCatalogCheck(t *testing.T) {
	good := CatalogEntry{ID: "temp-share", Name: "T", Repo: "o/r", Asset: `^t-(\d+\.\d+\.\d+)\.tar$`, Checksums: "{version}.json"}
	for name, mutate := range map[string]func(*CatalogEntry){
		"bad id":        func(e *CatalogEntry) { e.ID = "../x" },
		"bad repo":      func(e *CatalogEntry) { e.Repo = "o/r/../x" },
		"no group":      func(e *CatalogEntry) { e.Asset = `^t\.tar$` },
		"bad checksums": func(e *CatalogEntry) { e.Checksums = "../sums" },
	} {
		e := good
		mutate(&e)
		if err := (Catalog{Plugins: []CatalogEntry{e}}).Check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := (Catalog{Plugins: []CatalogEntry{good, good}}).Check(); err == nil {
		t.Error("a plugin twice was accepted")
	}
	if err := (Catalog{}).Check(); err == nil {
		t.Error("an empty list was accepted")
	}
}

// fakeGitHub serves plugins.json and two plugins' releases: one with a
// <version>.json checksum, one with SHA256SUMS.txt.
func fakeGitHub(t *testing.T, catalogStatus *int, archive []byte) *httptest.Server {
	sum := sha256.Sum256(archive)
	hexSum := hex.EncodeToString(sum[:])
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog":
			if *catalogStatus != 200 {
				w.WriteHeader(*catalogStatus)
				return
			}
			if r.Header.Get("Authorization") != "Bearer tok" {
				t.Errorf("no token: %q", r.Header.Get("Authorization"))
			}
			fmt.Fprint(w, `{"plugins":[
				{"id":"temp-share","name":"Temp Share","repo":"o/temp","asset":"^temp-share-([0-9.]+)\\.tar\\.gz$","checksums":"{version}.json"},
				{"id":"shiver","name":"Shiver","repo":"o/shiver","asset":"^shiver-plugin-([0-9.]+)\\.zip$","checksums":"SHA256SUMS.txt"}]}`)
		case "/repos/o/temp/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.0.5","assets":[{"name":"temp-share-0.0.5.tar.gz","browser_download_url":"%[1]s/a"},{"name":"0.0.5.json","browser_download_url":"%[1]s/j"}]}`, srv.URL)
		case "/repos/o/shiver/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.2.7","assets":[{"name":"shiver-plugin-0.3.2.zip","browser_download_url":"%[1]s/a"},{"name":"SHA256SUMS.txt","browser_download_url":"%[1]s/s"}]}`, srv.URL)
		case "/a":
			w.Write(archive)
		case "/j":
			fmt.Fprintf(w, `{"version":"0.0.5","checksum":%q,"sdkVersion":2}`, hexSum)
		case "/s":
			fmt.Fprintf(w, "%s *shiver-plugin-0.3.2.zip\n", hexSum)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testCatalogClient(t *testing.T, srv *httptest.Server) *CatalogClient {
	dir := t.TempDir()
	tok := filepath.Join(dir, "token")
	os.WriteFile(tok, []byte("tok\n"), 0o600)
	c := NewCatalogClient("test", tok, filepath.Join(dir, "cache.json"))
	c.URL, c.APIBase = srv.URL+"/catalog", srv.URL
	return c
}

func TestCatalogClient(t *testing.T) {
	status := 200
	archive := []byte("the plugin")
	srv := fakeGitHub(t, &status, archive)
	c := testCatalogClient(t, srv)
	ctx := context.Background()

	cat, err := c.Catalog(ctx)
	if err != nil || len(cat.Plugins) != 2 {
		t.Fatalf("%+v %v", cat, err)
	}
	for _, id := range []string{"temp-share", "shiver"} {
		e, _ := cat.Find(id)
		rel, err := c.Latest(ctx, e)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		want := map[string]string{"temp-share": "0.0.5", "shiver": "0.3.2"}[id]
		if rel.Version != want {
			t.Fatalf("%s: version %q", id, rel.Version)
		}
		file := filepath.Join(t.TempDir(), "x")
		if err := c.Download(ctx, rel, file); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if b, _ := os.ReadFile(file); string(b) != string(archive) {
			t.Fatalf("%s: %q", id, b)
		}
	}

	// Cached: the catalog still loads while GitHub is down.
	status = 500
	if _, err := c.Catalog(ctx); err != nil {
		t.Fatalf("cache: %v", err)
	}
	// Not cached: unavailable, saying so.
	c.CacheFile = ""
	_, err = c.Catalog(ctx)
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "Shiver plugins are unavailable") {
		t.Fatalf("%v", err)
	}
	status = 404
	if _, err := c.Catalog(ctx); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("404 should mention the token: %v", err)
	}
}

func TestCatalogDownloadChecksMismatch(t *testing.T) {
	status := 200
	srv := fakeGitHub(t, &status, []byte("the plugin"))
	c := testCatalogClient(t, srv)
	rel := CatalogRelease{AssetName: "temp-share-0.0.5.tar.gz", AssetURL: srv.URL + "/a", SumsName: "0.0.5.json", SumsURL: srv.URL + "/s"}
	// /s is a SHA256SUMS file, not the JSON the name promises.
	if err := c.Download(context.Background(), rel, filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("accepted a checksum file it couldn't read")
	}
	if _, err := checksumIn([]byte(`{"checksum":"00"}`), "1.json", "a"); err == nil {
		t.Fatal("accepted a short checksum")
	}
	if _, err := checksumIn([]byte(strings.Repeat("0", 64)+" *other\n"), "SHA256SUMS.txt", "a"); err == nil {
		t.Fatal("accepted another file's line")
	}
}
