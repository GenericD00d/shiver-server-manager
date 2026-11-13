package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

const musicManifest = `{"id":"music-bot","name":"Music bot","author":"x","description":"plays music","version":"1.2.0","sdkVersion":2}`

type tarFile struct {
	name, body string
	mode       int64
	typ        byte
	link       string
}

func makeTar(t *testing.T, files []tarFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		typ := f.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := f.mode
		if mode == 0 {
			mode = 0o644
		}
		h := &tar.Header{Name: f.name, Mode: mode, Size: int64(len(f.body)), Typeflag: typ, Linkname: f.link, Format: tar.FormatPAX}
		if typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			tw.Write([]byte(f.body))
		}
	}
	tw.Close()
	return buf.Bytes()
}

func musicBot() []tarFile {
	return []tarFile{
		{name: "music-bot/", typ: tar.TypeDir, mode: 0o755},
		{name: "music-bot/manifest.json", body: musicManifest},
		{name: "music-bot/server/index.js", body: "//"},
		{name: "music-bot/client/index.js", body: "//"},
		{name: "music-bot/bin/helper", body: "#!/bin/sh\n", mode: 0o755},
	}
}

func writeFile(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func gzipped(b []byte) []byte {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write(b)
	gw.Close()
	return buf.Bytes()
}

func zstded(t *testing.T, b []byte) []byte {
	var buf bytes.Buffer
	zw, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(zw, bytes.NewReader(b))
	zw.Close()
	return buf.Bytes()
}

func TestUnpackFileFormats(t *testing.T) {
	tarball := makeTar(t, musicBot())
	zipped := makeZip(t, map[string]string{
		"manifest.json": musicManifest, "server/index.js": "//", "client/index.js": "//", "bin/helper": "#!/bin/sh\n",
	}, map[string]fs.FileMode{"bin/helper": 0o755})
	for name, b := range map[string][]byte{
		"plugin.zip":     zipped,
		"plugin.tar":     tarball,
		"plugin.tar.gz":  gzipped(tarball),
		"plugin.tar.zst": zstded(t, tarball),
		"misnamed.zip":   gzipped(tarball), // told apart by contents
	} {
		t.Run(name, func(t *testing.T) {
			root, m, err := UnpackFile(writeFile(t, name, b), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if m.ID != "music-bot" || m.Valid(2) != nil {
				t.Fatalf("%+v %v", m, m.Valid(2))
			}
			if runtime.GOOS != "windows" {
				fi, err := os.Stat(filepath.Join(root, "bin", "helper"))
				if err != nil || fi.Mode().Perm() != 0o700 {
					t.Fatalf("helper: %v %v", fi, err)
				}
				fi, _ = os.Stat(filepath.Join(root, "manifest.json"))
				if fi.Mode().Perm() != 0o600 {
					t.Fatalf("manifest: %v", fi.Mode())
				}
			}
		})
	}
}

func TestUnpackFileRefusesUnsafeArchives(t *testing.T) {
	base := musicBot()
	for name, extra := range map[string]tarFile{
		"parent path":   {name: "../evil.js", body: "x"},
		"absolute path": {name: "/etc/evil", body: "x"},
		"symlink":       {name: "music-bot/link", typ: tar.TypeSymlink, link: "/etc/passwd"},
		"hard link":     {name: "music-bot/hard", typ: tar.TypeLink, link: "music-bot/manifest.json"},
		"device":        {name: "music-bot/dev", typ: tar.TypeChar},
	} {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			_, _, err := UnpackFile(writeFile(t, "p.tar", makeTar(t, append(append([]tarFile{}, base...), extra))), dest)
			if err == nil {
				t.Fatal("accepted")
			}
		})
	}
	if _, _, err := UnpackFile(writeFile(t, "p.txt", []byte(strings.Repeat("not an archive ", 50))), t.TempDir()); err == nil || !strings.Contains(err.Error(), "isn't a zip or tar") {
		t.Fatalf("%v", err)
	}
	empty := makeTar(t, []tarFile{{name: "README.md", body: "hi"}})
	if _, _, err := UnpackFile(writeFile(t, "p.tar", empty), t.TempDir()); err == nil || !strings.Contains(err.Error(), "doesn't hold a plugin") {
		t.Fatalf("%v", err)
	}
}

// The staging folder lives inside a folder the instance's own user owns, so
// that user can plant a symlink in it before root unpacks. A write through
// one must stay inside the folder rather than follow it out.
func TestUnpackDoesNotFollowSymlinksInDest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink ownership attack is a Unix concern")
	}
	outside := t.TempDir()
	marker := filepath.Join(outside, "marker")
	if err := os.WriteFile(marker, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	// dest/music-bot -> the attacker's target; the archive then writes
	// music-bot/manifest.json, which must not land on outside/.
	if err := os.Symlink(outside, filepath.Join(dest, "music-bot")); err != nil {
		t.Fatal(err)
	}
	_, _, err := UnpackFile(writeFile(t, "p.tar", makeTar(t, musicBot())), dest)
	if err == nil {
		t.Fatal("unpack followed a planted symlink out of dest")
	}
	if b, _ := os.ReadFile(marker); string(b) != "original" {
		t.Fatalf("a write escaped dest: marker is now %q", b)
	}
	if _, err := os.Stat(filepath.Join(outside, "manifest.json")); err == nil {
		t.Fatal("manifest.json was written outside dest")
	}
}

func TestManifestValid(t *testing.T) {
	for _, tc := range []struct {
		id   string
		sdk  int
		want bool
	}{
		{"music-bot", 2, true},
		{"a.b_c-1", 2, true},
		{"../x", 2, false},
		{"Music", 2, false},
		{"", 2, false},
		{"a..b", 2, false},
		{"music-bot", 1, false},
	} {
		m := Manifest{ID: tc.id, Version: "1.0.0", SDKVersion: 2}
		if got := m.Valid(tc.sdk) == nil; got != tc.want {
			t.Errorf("%q sdk %d: %v", tc.id, tc.sdk, got)
		}
	}
}
