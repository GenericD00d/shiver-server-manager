//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
)

// TestE2EBackupExportImport: milestone 8.
func TestE2EBackupExportImport(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	data := e.serverInfo(t, "chat-a")["dataPath"].(string)
	upload := filepath.Join(data, "uploads", "e2e-upload.txt")

	t.Run("backup and restore bring back an upload", func(t *testing.T) {
		uid, gid, _ := osuser.IDs(osuser.InstanceUser("chat-a"))
		os.MkdirAll(filepath.Dir(upload), 0o700)
		os.WriteFile(upload, []byte("hello from the e2e test"), 0o600)
		os.Chown(upload, uid, gid)
		out := e.stdout(t, "", "backup", "chat-a")
		file := strings.TrimSpace(out[strings.LastIndex(out, "-> ")+3:])
		if _, err := os.Stat(file); err != nil {
			t.Fatalf("backup file: %v\n%s", err, out)
		}
		if list := e.stdout(t, "", "backup", "list", "chat-a"); !strings.Contains(list, filepath.Base(file)) {
			t.Fatalf("backup list:\n%s", list)
		}
		os.Remove(upload)
		e.asAdmin(t, 0, e2ePass2+"\n", "restore", "chat-a", file, "--yes")
		if b, _ := os.ReadFile(upload); string(b) != "hello from the e2e test" {
			t.Fatal("upload not restored")
		}
		if err := sharkord.Healthz(ctx, e.port(t, "chat-a")); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("the timer's unattended backup needs no login", func(t *testing.T) {
		e.expect(t, 0, "", "", "backup", "--all", "--unattended")
		e.expect(t, 0, "", "", "backup", "--scheduled", "--unattended")
	})

	t.Run("a server's own schedule, retention and deleting a backup", func(t *testing.T) {
		e.asAdmin(t, 0, "", "backup", "chat-a")
		e.asAdmin(t, 0, "", "backup", "schedule", "chat-a", "--times", "03:30", "--days", "mon,thu", "--keep", "2", "--yes")
		var views []map[string]any
		if err := json.Unmarshal([]byte(e.stdout(t, "", "backup", "schedule", "chat-a", "--json")), &views); err != nil || len(views) != 1 {
			t.Fatalf("%v %v", views, err)
		}
		if v := views[0]; v["keepCount"] != float64(2) || v["backups"] != float64(2) || fmt.Sprint(v["times"]) != "[03:30]" || fmt.Sprint(v["days"]) != "[mon thu]" {
			t.Fatalf("%v", v)
		}
		var list map[string][]map[string]any
		json.Unmarshal([]byte(e.stdout(t, "", "backup", "list", "chat-a", "--json")), &list)
		e.asAdmin(t, 0, "", "backup", "delete", "chat-a", list["chat-a"][0]["file"].(string), "--yes")
		e.asAdmin(t, 1, "", "backup", "delete", "chat-a", "../../../etc/passwd", "--yes")
		json.Unmarshal([]byte(e.stdout(t, "", "backup", "list", "chat-a", "--json")), &list)
		if len(list["chat-a"]) != 1 {
			t.Fatalf("%v", list)
		}
		e.asAdmin(t, 0, "", "backup", "schedule", "chat-a", "--off")
		e.asAdmin(t, 0, "", "backup", "schedule", "chat-a", "--on", "--times", "default", "--days", "every", "--keep", "default")
	})

	exportFile := "/var/tmp/chat-b-export.tar.zst.age"
	const exportPass = "an export passphrase"
	t.Run("export with the token, import as a new server", func(t *testing.T) {
		e.asAdmin(t, 0, e2ePass2+"\n"+exportPass+"\n", "export", "chat-b", "--out", exportFile, "--include-token")
		e.asAdmin(t, 0, exportPass+"\n", "import", exportFile, "--id", "chat-m", "--domain", "chat-m.example.com", "--yes")
		if err := sharkord.Healthz(ctx, e.port(t, "chat-m")); err != nil {
			t.Fatal(err)
		}
		tokB := strings.TrimSpace(e.stdout(t, e2ePass2+"\n", "token", "show", "chat-b"))
		tokM := strings.TrimSpace(e.stdout(t, e2ePass2+"\n", "token", "show", "chat-m"))
		if tokB == "" || tokB != tokM {
			t.Fatal("the imported server's token differs")
		}
		if info := e.serverInfo(t, "chat-m"); info["encrypted"] != true || info["tls"] != "selfsigned" {
			t.Fatalf("imported record: %v", info)
		}
		e.asAdmin(t, ExitError, "wrong export passphrase\n", "import", exportFile, "--id", "chat-n", "--domain", "chat-n.example.com", "--yes")
	})

	t.Run("import a plain install's data folder", func(t *testing.T) {
		// A stand-in for ~/.config/sharkord: chat-b's data, copied while stopped.
		src := "/srv/plain-install/sharkord"
		e.asAdmin(t, 0, "", "instance", "stop", "chat-b")
		sh(t, "mkdir -p "+src+" && rsync -a /var/lib/sharkord/chat-b/ "+src+"/ && chown -R 1000:1000 "+src)
		e.asAdmin(t, 0, "", "instance", "start", "chat-b")
		os.WriteFile(src+"/config.ini", []byte("[server]\nallowedOrigins[]=https://old.example.com\n[webRtc]\nport=40000\nmaxBitrate=10000000\n"), 0o600)
		tok := strings.TrimSpace(e.stdout(t, e2ePass2+"\n", "token", "show", "chat-b"))
		e.asAdmin(t, 0, tok+"\n", "import", src, "--id", "chat-p", "--domain", "chat-p.example.com", "--no-encrypt", "--yes")
		info := e.serverInfo(t, "chat-p")
		settings, _ := info["settings"].(map[string]any)
		if settings["SHARKORD_WEBRTC_MAX_BITRATE"] != "10000000" || settings["SHARKORD_ALLOWED_ORIGINS"] != "https://old.example.com" {
			t.Fatalf("settings not carried over: %v", info["settings"])
		}
		if info["tokenSaved"] != true || info["healthy"] != true {
			t.Fatalf("%v", info)
		}
		uid, _, _ := osuser.IDs(osuser.InstanceUser("chat-p"))
		if out := sh(t, "find /var/lib/sharkord/chat-p ! -uid "+strconv.Itoa(uid)+" | head -3"); out != "" {
			t.Fatalf("files not handed to the new user: %s", out)
		}
	})

	t.Run("clean up", func(t *testing.T) {
		for _, id := range []string{"chat-m", "chat-p"} {
			e.asAdmin(t, 0, id+"\n", "instance", "delete", id)
		}
	})
}
