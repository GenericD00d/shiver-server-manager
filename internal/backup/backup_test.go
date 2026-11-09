package backup

import (
	"archive/tar"
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/klauspost/compress/zstd"
)

func makeData(t *testing.T) (dataPath, dbCopy string) {
	t.Helper()
	dataPath = t.TempDir()
	for p, body := range map[string]string{
		"config.ini":                   "[server]\nport=4991\n",
		"uploads/a.png":                "png",
		"public/x.txt":                 "x",
		"plugins/shiver/manifest.json": `{"id":"shiver"}`,
		"plugin-data/shiver/s.json":    "{}",
		"interface/0.0.30/index.html":  "skip me",
		"mediasoup/worker":             "skip me",
		"logs/combined.log":            "skip me",
		"backups/old.sqlite":           "skip me",
		"db.sqlite-wal":                "skip me",
	} {
		full := filepath.Join(dataPath, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o700)
		os.WriteFile(full, []byte(body), 0o600)
	}
	db, _ := sql.Open("sqlite", "file:"+filepath.Join(dataPath, "db.sqlite"))
	db.Exec(`PRAGMA journal_mode=WAL`)
	db.Exec(`CREATE TABLE settings (secret_token TEXT)`)
	db.Exec(`INSERT INTO settings VALUES ('hash')`)
	dbCopy = filepath.Join(t.TempDir(), "copy.sqlite")
	if err := Vacuum(filepath.Join(dataPath, "db.sqlite"), dbCopy); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return
}

func TestRoundTripWithVaultKey(t *testing.T) {
	data, dbCopy := makeData(t)
	id, _ := age.GenerateX25519Identity()
	var buf bytes.Buffer
	m := Manifest{Format: FormatVersion, Kind: "backup", SharkordVersion: "0.0.30"}
	m.Instance.ID = "chat-a"
	if err := Write(&buf, WriteOpts{Manifest: m, DataPath: data, DBCopy: dbCopy, Recipient: id.Recipient()}); err != nil {
		t.Fatal(err)
	}
	if k, err := Detect(bytes.NewReader(buf.Bytes())); err != nil || k != VaultKey {
		t.Fatalf("detect: %v %v", k, err)
	}
	dest := t.TempDir()
	got, err := Read(bytes.NewReader(buf.Bytes()), ReadOpts{Identity: id, Dest: dest})
	if err != nil {
		t.Fatal(err)
	}
	if got.Instance.ID != "chat-a" || got.SharkordVersion != "0.0.30" {
		t.Fatalf("manifest %+v", got)
	}
	for _, want := range []string{"config.ini", "uploads/a.png", "public/x.txt", "plugins/shiver/manifest.json", "plugin-data/shiver/s.json", "db.sqlite"} {
		if _, err := os.Stat(filepath.Join(dest, want)); err != nil {
			t.Errorf("missing %s", want)
		}
	}
	for _, skipped := range []string{"interface", "mediasoup", "logs", "backups", "db.sqlite-wal"} {
		if _, err := os.Stat(filepath.Join(dest, skipped)); err == nil {
			t.Errorf("%s should be left out", skipped)
		}
	}
	db, _ := sql.Open("sqlite", "file:"+filepath.Join(dest, "db.sqlite"))
	defer db.Close()
	var tok string
	if err := db.QueryRow(`SELECT secret_token FROM settings`).Scan(&tok); err != nil || tok != "hash" {
		t.Fatalf("database: %q %v", tok, err)
	}
	other, _ := age.GenerateX25519Identity()
	if _, err := Read(bytes.NewReader(buf.Bytes()), ReadOpts{Identity: other, Dest: t.TempDir()}); err == nil {
		t.Fatal("read with the wrong key")
	}
}

func TestExportWithPassphrase(t *testing.T) {
	data, dbCopy := makeData(t)
	r, _ := age.NewScryptRecipient("export passphrase!")
	r.SetWorkFactor(10)
	var buf bytes.Buffer
	if err := Write(&buf, WriteOpts{Manifest: Manifest{Format: 1, Kind: "export"}, DataPath: data, DBCopy: dbCopy, Recipient: r}); err != nil {
		t.Fatal(err)
	}
	if k, _ := Detect(bytes.NewReader(buf.Bytes())); k != Passphrase {
		t.Fatal("not detected as an export")
	}
	wrong, _ := age.NewScryptIdentity("nope nope nope")
	if _, err := Read(bytes.NewReader(buf.Bytes()), ReadOpts{Identity: wrong, Dest: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "wrong passphrase") {
		t.Fatalf("wrong passphrase: %v", err)
	}
	right, _ := age.NewScryptIdentity("export passphrase!")
	if _, err := Read(bytes.NewReader(buf.Bytes()), ReadOpts{Identity: right, Dest: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
}

// evil builds an archive by hand, encrypted to id.
func evil(t *testing.T, id *age.X25519Identity, hdrs ...*tar.Header) []byte {
	var buf bytes.Buffer
	enc, _ := age.Encrypt(&buf, id.Recipient())
	zw, _ := zstd.NewWriter(enc)
	tw := tar.NewWriter(zw)
	tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("{}"))
	for _, h := range hdrs {
		tw.WriteHeader(h)
		if h.Size > 0 {
			tw.Write(make([]byte, h.Size))
		}
	}
	tw.Close()
	zw.Close()
	enc.Close()
	return buf.Bytes()
}

func TestUnsafeEntriesRefused(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	for name, h := range map[string]*tar.Header{
		"dotdot":    {Name: "data/../../etc/passwd", Size: 1, Typeflag: tar.TypeReg, Mode: 0o600},
		"absolute":  {Name: "/etc/passwd", Size: 1, Typeflag: tar.TypeReg, Mode: 0o600},
		"symlink":   {Name: "data/link", Linkname: "/etc/shadow", Typeflag: tar.TypeSymlink},
		"hardlink":  {Name: "data/hard", Linkname: "data/x", Typeflag: tar.TypeLink},
		"device":    {Name: "data/dev", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3},
		"outside":   {Name: "other/file", Size: 1, Typeflag: tar.TypeReg, Mode: 0o600},
		"backslash": {Name: "data/..\\..\\x", Size: 1, Typeflag: tar.TypeReg, Mode: 0o600},
	} {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			if _, err := Read(bytes.NewReader(evil(t, id, h)), ReadOpts{Identity: id, Dest: dest}); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func FuzzSafeTarget(f *testing.F) {
	for _, s := range []string{"data/a", "data/../x", "db.sqlite", "data/uploads/../../../etc"} {
		f.Add(s)
	}
	dest := filepath.FromSlash("/dest")
	f.Fuzz(func(t *testing.T, name string) {
		got, err := safeTarget(dest, &tar.Header{Name: name, Typeflag: tar.TypeReg})
		if err != nil || got == "" {
			return
		}
		rel, rerr := filepath.Rel(dest, got)
		if rerr != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			t.Fatalf("%q escapes to %q", name, got)
		}
	})
}

func TestRecordKeepsN(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i) * 12 * time.Hour)
		name := FileName(ts)
		os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600)
		if _, err := Record(dir, Entry{File: name, CreatedAt: ts}, 3); err != nil {
			t.Fatal(err)
		}
	}
	ix, _ := LoadIndex(dir)
	if len(ix.Backups) != 3 || ix.Backups[0].File != FileName(base.Add(24*time.Hour)) {
		t.Fatalf("%+v", ix.Backups)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.age"))
	if len(files) != 3 {
		t.Fatalf("files %v", files)
	}
}
