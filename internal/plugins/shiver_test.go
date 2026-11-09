package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func makeZip(t *testing.T, files map[string]string, modes map[string]fs.FileMode) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if m, ok := modes[name]; ok {
			h.SetMode(m)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

const manifest = `{"id":"shiver","name":"Shiver","author":"Shiver","description":"x","version":"0.3.2","sdkVersion":2}`

func goodZip(t *testing.T) []byte {
	return makeZip(t, map[string]string{
		"shiver/manifest.json":   manifest,
		"shiver/server/index.js": "//",
		"shiver/client/index.js": "//",
		"shiver/README.md":       "readme",
	}, nil)
}

func TestUnpack(t *testing.T) {
	root, m, err := Unpack(goodZip(t), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(root) != "shiver" || m.Version != "0.3.2" || m.Check(2) != nil {
		t.Fatalf("%s %+v", root, m)
	}
	if m.Check(1) == nil {
		t.Fatal("SDK mismatch accepted")
	}
	// Files at the top level work too.
	flat := makeZip(t, map[string]string{"manifest.json": manifest, "server/index.js": "", "client/index.js": ""}, nil)
	if _, _, err := Unpack(flat, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestUnpackRefusesUnsafeZips(t *testing.T) {
	for name, z := range map[string][]byte{
		"dotdot":    makeZip(t, map[string]string{"../evil.js": "x"}, nil),
		"absolute":  makeZip(t, map[string]string{"/etc/evil": "x"}, nil),
		"symlink":   makeZip(t, map[string]string{"shiver/link": "/etc/shadow"}, map[string]fs.FileMode{"shiver/link": fs.ModeSymlink | 0o777}),
		"no plugin": makeZip(t, map[string]string{"readme.txt": "x"}, nil),
		"two":       makeZip(t, map[string]string{"a/manifest.json": manifest, "a/server/index.js": "", "a/client/index.js": "", "b/manifest.json": manifest, "b/server/index.js": "", "b/client/index.js": ""}, nil),
		"wrong id":  makeZip(t, map[string]string{"manifest.json": strings.Replace(manifest, `"shiver"`, `"other"`, 1), "server/index.js": "", "client/index.js": ""}, nil),
	} {
		t.Run(name, func(t *testing.T) {
			_, m, err := Unpack(z, t.TempDir())
			if err == nil && m.Check(2) == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestLatestAndDownload(t *testing.T) {
	zb := goodZip(t)
	sum := sha256.Sum256(zb)
	sums := fmt.Sprintf("0a09 *Shiver_0.2.7_arm64.apk\n%s *shiver-plugin-0.3.2.zip\n", hex.EncodeToString(sum[:]))
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repo/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.2.7","assets":[{"name":"Shiver_0.2.7_arm64.apk","browser_download_url":"%[1]s/apk"},{"name":"shiver-plugin-0.3.2.zip","browser_download_url":"%[1]s/zip"},{"name":"SHA256SUMS.txt","browser_download_url":"%[1]s/sums"}]}`, srv.URL)
		case "/zip":
			w.Write(zb)
		case "/sums":
			fmt.Fprint(w, sums)
		}
	}))
	defer srv.Close()
	c := NewClient("test", filepath.Join(t.TempDir(), "cache.json"))
	c.APIBase = srv.URL + "/repo"
	r, err := c.Latest(context.Background())
	if err != nil || r.PluginVersion() != "0.3.2" || r.Tag != "v0.2.7" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := c.Download(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	sums = strings.Replace(sums, hex.EncodeToString(sum[:]), strings.Repeat("0", 64), 1)
	if _, err := c.Download(context.Background(), r); err == nil {
		t.Fatal("bad checksum accepted")
	}
}

func TestEnableInDB(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db.sqlite")
	db, _ := sql.Open("sqlite", "file:"+p)
	db.Exec(`CREATE TABLE settings (name TEXT, enable_plugins INTEGER NOT NULL DEFAULT 0)`)
	db.Exec(`INSERT INTO settings (name) VALUES ('x')`)
	db.Exec(`CREATE TABLE plugin_data (plugin_id TEXT PRIMARY KEY NOT NULL, enabled INTEGER NOT NULL DEFAULT 0, settings TEXT NOT NULL DEFAULT '{}', version TEXT)`)
	db.Exec(`INSERT INTO plugin_data (plugin_id, enabled) VALUES ('shiver', 0)`)
	db.Close()
	for i := 0; i < 2; i++ { // twice: idempotent
		if err := EnableInDB(p); err != nil {
			t.Fatal(err)
		}
	}
	db, _ = sql.Open("sqlite", "file:"+p)
	defer db.Close()
	var on, enabled int
	db.QueryRow(`SELECT enable_plugins FROM settings`).Scan(&on)
	db.QueryRow(`SELECT enabled FROM plugin_data WHERE plugin_id='shiver'`).Scan(&enabled)
	if on != 1 || enabled != 1 {
		t.Fatalf("enable_plugins=%d enabled=%d", on, enabled)
	}
	old := filepath.Join(t.TempDir(), "old.sqlite")
	db2, _ := sql.Open("sqlite", "file:"+old)
	db2.Exec(`CREATE TABLE settings (name TEXT)`)
	db2.Close()
	if err := EnableInDB(old); err == nil {
		t.Fatal("missing columns not caught")
	}
}
