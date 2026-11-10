package ops

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
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

func shiverZip(version string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// A fixed order and time, so every request gets the same bytes.
	for _, f := range [][2]string{
		{"shiver/manifest.json", fmt.Sprintf(`{"id":"shiver","name":"Shiver","author":"Shiver","description":"x","version":%q,"sdkVersion":2}`, version)},
		{"shiver/server/index.js", "//"},
		{"shiver/client/index.js", "//"},
	} {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: f[0], Method: zip.Deflate, Modified: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
		w.Write([]byte(f[1]))
	}
	zw.Close()
	return buf.Bytes()
}

// fakeShiver serves a release whose plugin version can be changed.
func fakeShiver(t *testing.T, h *harness) *string {
	version := "0.3.2"
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zb := shiverZip(version)
		sum := sha256.Sum256(zb)
		name := "shiver-plugin-" + version + ".zip"
		switch r.URL.Path {
		case "/repo/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.2.7","assets":[{"name":%q,"browser_download_url":"%s/zip"},{"name":"SHA256SUMS.txt","browser_download_url":"%s/sums"}]}`, name, srv.URL, srv.URL)
		case "/zip":
			w.Write(zb)
		case "/sums":
			fmt.Fprintf(w, "%s *%s\n", hex.EncodeToString(sum[:]), name)
		}
	}))
	t.Cleanup(srv.Close)
	h.s.Shiver = plugins.NewClient("test", "")
	h.s.Shiver.APIBase = srv.URL + "/repo"
	return &version
}

func TestInstallAndUpdateShiver(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	version := fakeShiver(t, h)
	journal := ""
	h.s.PluginLog = func(context.Context, string, time.Time) (string, error) { return journal, nil }
	data := h.s.St.Instances["chat-a"].DataPath

	journal = "[info] Plugin loaded: shiver@v0.3.2 by Shiver"
	note, err := h.s.InstallShiver(ctx, "chat-a")
	if err != nil {
		t.Fatal(err)
	}
	// The harness database has no plugin columns, so the switches can't be
	// set there and the admin is told where to switch them on.
	if !strings.Contains(note, "Settings -> Plugins") {
		t.Fatalf("note %q", note)
	}
	if m, ok := plugins.Installed(data); !ok || m.Version != "0.3.2" {
		t.Fatalf("installed %+v %v", m, ok)
	}
	if list, _ := h.s.Backups("chat-a"); len(list) != 1 {
		t.Fatal("no backup before installing")
	}
	if _, err := h.s.InstallShiver(ctx, "chat-a"); err == nil {
		t.Fatal("reinstalling the same version succeeded")
	}

	// An update Sharkord can't load puts the old copy back.
	*version = "0.3.3"
	journal = "[error] Failed to load plugin shiver: SyntaxError: boom"
	if _, err := h.s.InstallShiver(ctx, "chat-a"); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
	if m, _ := plugins.Installed(data); m.Version != "0.3.2" {
		t.Fatalf("old copy not put back: %+v", m)
	}
	journal = "[info] Plugin loaded: shiver@v0.3.3 by Shiver"
	if _, err := h.s.InstallShiver(ctx, "chat-a"); err != nil {
		t.Fatal(err)
	}
	if m, _ := plugins.Installed(data); m.Version != "0.3.3" {
		t.Fatal("not updated")
	}
	if entries, _ := os.ReadDir(filepath.Join(data, "plugins")); len(entries) != 1 {
		t.Fatalf("leftovers in plugins/: %v", entries)
	}
}

func pluginTarGz(t *testing.T, id, version string) string {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, f := range [][2]string{
		{id + "/manifest.json", fmt.Sprintf(`{"id":%q,"name":"Music bot","author":"x","description":"x","version":%q,"sdkVersion":2}`, id, version)},
		{id + "/server/index.js", "//"},
		{id + "/client/index.js", "//"},
	} {
		tw.WriteHeader(&tar.Header{Name: f[0], Mode: 0o644, Size: int64(len(f[1])), Typeflag: tar.TypeReg})
		tw.Write([]byte(f[1]))
	}
	tw.Close()
	gw.Close()
	p := filepath.Join(t.TempDir(), id+".tar.gz")
	os.WriteFile(p, buf.Bytes(), 0o600)
	return p
}

func TestInstallPluginFile(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	journal := "[info] Plugin loaded: music-bot@v1.0.0 by x"
	h.s.PluginLog = func(context.Context, string, time.Time) (string, error) { return journal, nil }
	data := h.s.St.Instances["chat-a"].DataPath

	m, _, err := h.s.InstallPluginFile(ctx, "chat-a", pluginTarGz(t, "music-bot", "1.0.0"))
	if err != nil || m.ID != "music-bot" {
		t.Fatalf("%+v %v", m, err)
	}
	if got, ok := plugins.InstalledPlugin(data, "music-bot"); !ok || got.Version != "1.0.0" {
		t.Fatalf("%+v %v", got, ok)
	}
	// An update that doesn't load puts 1.0.0 back.
	journal = "[error] Failed to load plugin music-bot: boom"
	if _, _, err := h.s.InstallPluginFile(ctx, "chat-a", pluginTarGz(t, "music-bot", "1.1.0")); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("%v", err)
	}
	if got, _ := plugins.InstalledPlugin(data, "music-bot"); got.Version != "1.0.0" {
		t.Fatalf("not put back: %+v", got)
	}
	// "music-bot" failing mustn't be read as "music-bot-2" failing, or the
	// other way round.
	journal = "[error] Failed to load plugin music-bot: old\n[info] Plugin loaded: music-bot-2@v1.0.0 by x"
	if _, _, err := h.s.InstallPluginFile(ctx, "chat-a", pluginTarGz(t, "music-bot-2", "1.0.0")); err != nil {
		t.Fatal(err)
	}
	if list, _ := h.s.Plugins("chat-a"); len(list) != 2 {
		t.Fatalf("%+v", list)
	}
	if _, _, err := h.s.InstallPluginFile(ctx, "chat-a", pluginTarGz(t, "Bad..Id", "1.0.0")); err == nil {
		t.Fatal("a bad id was installed")
	}
}
