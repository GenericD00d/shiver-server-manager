package ops

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

	"github.com/GenericD00d/shiver-server-manager/internal/plugins"
)

// fakeCatalog serves a plugins.json with temp-share, whose latest release
// (the version can be changed) holds the plugin with the given id, and
// media-player at the same version.
func fakeCatalog(t *testing.T, h *harness) (version, archiveID *string) {
	v, id := "0.0.5", "temp-share"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := os.ReadFile(pluginTarGz(t, id, v))
		sum := sha256.Sum256(b)
		switch r.URL.Path {
		case "/catalog":
			fmt.Fprint(w, `{"plugins":[{"id":"temp-share","name":"Temp Share","repo":"o/temp","asset":"^temp-share-([0-9.]+)\\.tar\\.gz$","checksums":"{version}.json"},`+
				`{"id":"media-player","name":"Media Player","repo":"o/media","asset":"^media-player-([0-9.]+)\\.tar\\.gz$","checksums":"{version}.json"}]}`)
		case "/repos/o/temp/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v%[2]s","assets":[{"name":"temp-share-%[2]s.tar.gz","browser_download_url":"%[1]s/a"},{"name":"%[2]s.json","browser_download_url":"%[1]s/j"}]}`, srv.URL, v)
		case "/a":
			w.Write(b)
		case "/j":
			fmt.Fprintf(w, `{"checksum":%q}`, hex.EncodeToString(sum[:]))
		case "/repos/o/media/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v%[2]s","assets":[{"name":"media-player-%[2]s.tar.gz","browser_download_url":"%[1]s/m"},{"name":"%[2]s.json","browser_download_url":"%[1]s/mj"}]}`, srv.URL, v)
		case "/m", "/mj":
			mb, _ := os.ReadFile(pluginTarGz(t, "media-player", v))
			if r.URL.Path == "/m" {
				w.Write(mb)
				return
			}
			msum := sha256.Sum256(mb)
			fmt.Fprintf(w, `{"checksum":%q}`, hex.EncodeToString(msum[:]))
		}
	}))
	t.Cleanup(srv.Close)
	h.s.Catalog = plugins.NewCatalogClient("test", "", "")
	h.s.Catalog.URL, h.s.Catalog.APIBase = srv.URL+"/catalog", srv.URL
	return &v, &id
}

func TestInstallFromCatalog(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for _, id := range []string{"chat-a", "chat-b"} {
		if err := h.s.Create(ctx, req(id)); err != nil {
			t.Fatal(err)
		}
	}
	version, archiveID := fakeCatalog(t, h)
	journal := "[info] Plugin loaded: temp-share@v0.0.5 by x"
	h.s.PluginLog = func(context.Context, string, time.Time) (string, error) { return journal, nil }

	ms, _, err := h.s.InstallFromCatalog(ctx, "chat-a", "temp-share")
	if err != nil || len(ms) != 1 || ms[0].Version != "0.0.5" {
		t.Fatalf("%+v %v", ms, err)
	}
	data := h.s.St.Instances["chat-a"].DataPath
	if got, ok := plugins.InstalledPlugin(data, "temp-share"); !ok || got.Version != "0.0.5" {
		t.Fatalf("%+v %v", got, ok)
	}
	// Nothing left behind in the data folder.
	if left, _ := filepath.Glob(filepath.Join(data, ".ssm-plugin-*")); len(left) != 0 {
		t.Fatalf("staging left: %v", left)
	}
	if _, _, err := h.s.InstallFromCatalog(ctx, "chat-a", "temp-share"); err == nil || !strings.Contains(err.Error(), "already has") {
		t.Fatalf("%v", err)
	}
	if _, _, err := h.s.InstallFromCatalog(ctx, "chat-a", "nope"); err == nil || !strings.Contains(err.Error(), "no Shiver plugin") {
		t.Fatalf("%v", err)
	}

	// A newer release shows as an update on chat-a only.
	*version = "0.0.6"
	cat, err := h.s.PluginCatalog(ctx)
	if err != nil || len(cat) != 2 || cat[0].Latest != "0.0.6" {
		t.Fatalf("%+v %v", cat, err)
	}
	for _, sv := range cat[0].Servers {
		want := sv.Instance == "chat-a"
		if sv.Update != want || (sv.Installed != "") != want {
			t.Fatalf("%+v", sv)
		}
	}

	// A release holding another plugin is refused before anything changes.
	*archiveID = "evil"
	if _, _, err := h.s.InstallFromCatalog(ctx, "chat-b", "temp-share"); err == nil || !strings.Contains(err.Error(), "not \"temp-share\"") {
		t.Fatalf("%v", err)
	}
	if _, ok := plugins.InstalledPlugin(h.s.St.Instances["chat-b"].DataPath, "evil"); ok {
		t.Fatal("installed the wrong plugin")
	}
}

// Several plugins go in with one stop and start, all or none.
func TestInstallSeveralFromCatalog(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	fakeCatalog(t, h)
	data := h.s.St.Instances["chat-a"].DataPath
	stops := func() (n int) {
		for _, l := range h.fake.Lines() {
			if strings.HasPrefix(l, "systemctl stop") {
				n++
			}
		}
		return n
	}

	// media-player fails to load: temp-share comes out again too.
	journal := "[info] Plugin loaded: temp-share@v0.0.5 by x\n[error] Failed to load plugin media-player: boom"
	h.s.PluginLog = func(context.Context, string, time.Time) (string, error) { return journal, nil }
	if _, _, err := h.s.InstallFromCatalog(ctx, "chat-a", "temp-share", "media-player"); err == nil || !strings.Contains(err.Error(), "boom") || strings.Count(err.Error(), "was removed again") != 2 {
		t.Fatalf("%v", err)
	}
	if list, _ := h.s.Plugins("chat-a"); len(list) != 0 {
		t.Fatalf("not put back: %+v", list)
	}

	journal = "[info] Plugin loaded: temp-share@v0.0.5 by x\n[info] Plugin loaded: media-player@v0.0.5 by x"
	before := stops()
	ms, _, err := h.s.InstallFromCatalog(ctx, "chat-a", "temp-share", "media-player", "temp-share")
	if err != nil || len(ms) != 2 {
		t.Fatalf("%+v %v", ms, err)
	}
	if n := stops() - before; n != 1 {
		t.Fatalf("stopped %d times, want once", n)
	}
	for _, id := range []string{"temp-share", "media-player"} {
		if got, ok := plugins.InstalledPlugin(data, id); !ok || got.Version != "0.0.5" {
			t.Fatalf("%s: %+v %v", id, got, ok)
		}
	}
	// Up to date: skipped, and with nothing left it's an error.
	if _, _, err := h.s.InstallFromCatalog(ctx, "chat-a", "temp-share", "media-player"); err == nil || !strings.Contains(err.Error(), "already has") {
		t.Fatalf("%v", err)
	}
}
