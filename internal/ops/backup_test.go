package ops

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// Root unpacks and swaps data through staging folders inside the instance's
// data folder, so those must stay root's: only the folder internal-vacuum
// writes the database copy into is given to the instance's user.
func TestStagingFoldersStayRootOwned(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	var given []string
	h.s.Chown = func(p string, uid, gid int) error {
		given = append(given, filepath.Base(p))
		return nil
	}
	staging, err := h.s.stagingDir("chat-a", "restore")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(staging)
	if len(given) != 0 {
		t.Fatalf("an unpack staging folder was given to the instance: %q", given)
	}
	if fi, err := os.Stat(staging); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("staging folder: %v %v", fi, err)
	}
	vacuum, err := h.s.vacuumDir("chat-a")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(vacuum)
	if len(given) != 1 || given[0] != filepath.Base(vacuum) {
		t.Fatalf("the vacuum folder should be the instance's, got %q", given)
	}
}

func TestBackupAndRestore(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	data := h.s.St.Instances["chat-a"].DataPath
	os.MkdirAll(filepath.Join(data, "uploads"), 0o700)
	os.WriteFile(filepath.Join(data, "uploads", "cat.png"), []byte("meow"), 0o600)

	file, err := h.s.Backup(ctx, "chat-a")
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := h.s.Backups("chat-a"); len(list) != 1 || list[0].SHA256 == "" {
		t.Fatalf("index %+v", list)
	}
	// Lose an upload and change the database.
	os.Remove(filepath.Join(data, "uploads", "cat.png"))
	db, _ := sql.Open("sqlite", "file:"+sharkord.DBPath(data))
	db.Exec(`UPDATE settings SET secret_token = 'changed'`)
	db.Close()

	key, _ := h.s.Vault.Unlock("test vault passphrase")
	old, err := h.s.Restore(ctx, "chat-a", file, key.Identity())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(data, "uploads", "cat.png")); string(b) != "meow" {
		t.Fatal("upload not restored")
	}
	if err := sharkord.CheckToken(data, testToken); err != nil {
		t.Fatalf("database not restored: %v", err)
	}
	if !strings.Contains(old, ".ssm-old-") {
		t.Fatalf("old copy %q", old)
	}
	if err := h.s.DeleteLeftover("chat-a", old); err != nil {
		t.Fatal(err)
	}
	if err := h.s.DeleteLeftover("chat-a", data); err == nil {
		t.Fatal("deleted the data folder itself")
	}

	// A restore that doesn't start puts the previous data back.
	os.WriteFile(filepath.Join(data, "marker"), []byte("current"), 0o600)
	h.healthy = false
	if _, err := h.s.Restore(ctx, "chat-a", file, key.Identity()); err == nil {
		t.Fatal("failed restore not reported")
	}
	if b, _ := os.ReadFile(filepath.Join(data, "marker")); string(b) != "current" {
		t.Fatal("previous data not put back")
	}
}

func TestExportThenImport(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	if err := h.s.Set(ctx, "chat-a", []string{"WEBRTC_MAX_BITRATE=20000000"}); err != nil {
		t.Fatal(err)
	}
	data := h.s.St.Instances["chat-a"].DataPath
	os.MkdirAll(filepath.Join(data, "uploads"), 0o700)
	os.WriteFile(filepath.Join(data, "uploads", "cat.png"), []byte("meow"), 0o600)

	out := filepath.Join(t.TempDir(), "chat-a.tar.zst.age")
	if err := h.s.Export(ctx, "chat-a", out, "export passphrase", true); err == nil {
		t.Fatal("token export without the vault unlocked")
	}
	h.s.VaultKey, _ = h.s.Vault.Unlock("test vault passphrase")
	if err := h.s.Export(ctx, "chat-a", out, "short", false); err == nil {
		t.Fatal("short passphrase accepted")
	}
	if err := h.s.Export(ctx, "chat-a", out, "export passphrase", true); err != nil {
		t.Fatal(err)
	}

	id, err := h.s.IdentityFor(out, func() (string, error) { return "export passphrase", nil })
	if err != nil {
		t.Fatal(err)
	}
	src := ImportSource{Archive: out, Identity: id}
	plan, err := h.s.PlanImport(ctx, src, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Settings["SHARKORD_WEBRTC_MAX_BITRATE"] != "20000000" {
		t.Fatalf("settings %v", plan.Settings)
	}
	r := ImportRequest{CreateRequest: CreateRequest{ID: "chat-z", Domain: "chat-z.example.com"}, Source: src, Plan: plan}
	if err := h.s.Import(ctx, r, ImportHooks{}); err != nil {
		t.Fatal(err)
	}
	z := h.s.St.Instances["chat-z"]
	if z.Status != "active" || z.Settings["SHARKORD_WEBRTC_MAX_BITRATE"] != "20000000" {
		t.Fatalf("imported record %+v", z)
	}
	if b, _ := os.ReadFile(filepath.Join(z.DataPath, "uploads", "cat.png")); string(b) != "meow" {
		t.Fatal("upload not imported")
	}
	if !h.s.Vault.HasSecret("chat-z", vault.OwnerToken) {
		t.Fatal("token from the export not saved")
	}
	if h.firstStart != 1 {
		t.Fatal("an import ran a first start")
	}
}

func TestImportFolder(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	src := t.TempDir()
	db, _ := sql.Open("sqlite", "file:"+filepath.Join(src, "db.sqlite"))
	db.Exec(`CREATE TABLE settings (secret_token TEXT)`)
	db.Exec(`INSERT INTO settings VALUES (?)`, sharkord.HashToken(testToken))
	db.Close()
	os.MkdirAll(filepath.Join(src, "interface", "0.0.29"), 0o700)
	os.WriteFile(filepath.Join(src, "config.ini"), []byte("[server]\nallowedOrigins[]=https://a.example.com\n"), 0o600)
	os.MkdirAll(filepath.Join(src, "uploads"), 0o700)
	os.WriteFile(filepath.Join(src, "uploads", "x"), []byte("x"), 0o600)

	plan, err := h.s.PlanImport(ctx, ImportSource{Folder: src}, "")
	if err != nil {
		t.Fatal(err)
	}
	if plan.DataVersion != "0.0.29" || plan.RunVersion != "0.0.30" || plan.Settings["SHARKORD_ALLOWED_ORIGINS"] != "https://a.example.com" {
		t.Fatalf("plan %+v", plan)
	}
	// a release picked instead of the newest; a pre-release isn't one
	if picked, err := h.s.PlanImport(ctx, ImportSource{Folder: src}, "0.0.29"); err != nil || picked.RunVersion != "0.0.29" {
		t.Fatalf("picked: %+v %v", picked, err)
	}
	if _, err := h.s.PlanImport(ctx, ImportSource{Folder: src}, "0.0.99"); err == nil {
		t.Fatal("a pre-release was accepted")
	}
	asked := 0
	r := ImportRequest{CreateRequest: CreateRequest{ID: "old", Domain: "old.example.com"}, Source: ImportSource{Folder: src}, Plan: plan}
	err = h.s.Import(ctx, r, ImportHooks{Token: func() (string, error) {
		asked++
		if asked == 1 {
			return "019a0000-0000-7000-8000-000000000000", nil // wrong
		}
		return testToken, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if asked != 2 || !h.s.Vault.HasSecret("old", vault.OwnerToken) {
		t.Fatalf("token asked %d times", asked)
	}
	if _, err := os.Stat(filepath.Join(h.s.St.Instances["old"].DataPath, "interface")); err == nil {
		t.Fatal("interface/ was copied")
	}

	os.MkdirAll(filepath.Join(src, "interface", "9.9.9"), 0o700)
	if _, err := h.s.PlanImport(ctx, ImportSource{Folder: src}, ""); err == nil {
		t.Fatal("data from a newer Sharkord accepted")
	}
}
