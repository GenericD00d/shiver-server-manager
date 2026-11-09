package ops

import (
	"archive/zip"
	"bytes"
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
