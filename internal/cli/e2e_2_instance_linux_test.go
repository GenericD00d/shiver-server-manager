//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
)

// TestE2EPlainInstance: milestone 3. A real Sharkord, created through the
// manager, answers /healthz, and the saved token matches its database.
func TestE2EPlainInstance(t *testing.T) {
	e := newE2E(t)
	const id, data = "chat-a", "/var/lib/sharkord/chat-a"
	var token string

	t.Run("create", func(t *testing.T) {
		e.asAdmin(t, 0, "", "instance", "create", "--id", id, "--domain", "chat-a.example.com", "--no-encrypt")
		if err := sharkord.Healthz(context.Background(), 5000); err != nil {
			t.Fatal(err)
		}
		uid, gid, _ := osuser.IDs(osuser.InstanceUser(id))
		ou, og, _ := fsutil.OwnerOf(data)
		fi, _ := os.Stat(data)
		if ou != uid || og != gid || fi.Mode().Perm() != 0o700 {
			t.Fatalf("data folder owner %d:%d mode %v", ou, og, fi.Mode().Perm())
		}
	})

	t.Run("saved token matches the database", func(t *testing.T) {
		token = strings.TrimSpace(e.stdout(t, e2ePass2+"\n", "token", "show", id))
		if err := sharkord.CheckToken(data, token); err != nil {
			t.Fatalf("token %q: %v", token, err)
		}
		journal, _ := exec.Command("journalctl", "-u", "sharkord@"+id, "--no-pager").CombinedOutput()
		if strings.Contains(string(journal), token) {
			t.Fatal("the token reached the journal")
		}
		log, _ := os.ReadFile("/var/log/shiver-server-manager.log")
		if strings.Contains(string(log), token) {
			t.Fatal("the token reached the manager's log")
		}
	})

	t.Run("token save checks the database", func(t *testing.T) {
		e.asAdmin(t, ExitError, "019a0000-0000-7000-8000-000000000000\n", "token", "save", id)
		e.asAdmin(t, 0, token+"\n", "token", "save", id)
	})

	t.Run("token new replaces the token", func(t *testing.T) {
		e.asAdmin(t, 0, "", "token", "new", id, "--yes")
		if err := sharkord.Healthz(context.Background(), 5000); err != nil {
			t.Fatal(err)
		}
		newTok := strings.TrimSpace(e.stdout(t, e2ePass2+"\n", "token", "show", id))
		if newTok == token {
			t.Fatal("the token didn't change")
		}
		if err := sharkord.CheckToken(data, newTok); err != nil {
			t.Fatalf("new token: %v", err)
		}
		if err := sharkord.CheckToken(data, token); err == nil {
			t.Fatal("the old token still works")
		}
		// Written as the server's user: nothing root-owned beside the database.
		uid, _, _ := osuser.IDs(osuser.InstanceUser(id))
		files, _ := filepath.Glob(data + "/db.sqlite*")
		for _, f := range files {
			if ou, _, _ := fsutil.OwnerOf(f); ou != uid {
				t.Errorf("%s is owned by %d", f, ou)
			}
		}
		token = newTok
	})

	t.Run("list", func(t *testing.T) {
		var infos []map[string]any
		if err := json.Unmarshal([]byte(e.stdout(t, "", "instance", "list", "--json")), &infos); err != nil {
			t.Fatal(err)
		}
		if len(infos) != 1 || infos[0]["id"] != id || infos[0]["healthy"] != true || infos[0]["tokenSaved"] != true {
			t.Fatalf("%v", infos)
		}
	})

	t.Run("a digits-only string setting keeps Sharkord starting", func(t *testing.T) {
		// Written bare, 12345 would parse as a number, fail Sharkord's
		// checks and stop it starting (guide, "Prototype first").
		e.asAdmin(t, 0, "", "instance", "set", id, "OIDC_CLIENT_ID=12345", "WEBRTC_MAX_BITRATE=20000000")
		if err := sharkord.Healthz(context.Background(), 5000); err != nil {
			t.Fatal(err)
		}
		ini, _ := os.ReadFile(data + "/config.ini")
		if strings.Contains(string(ini), "12345") {
			t.Fatal("an env setting was written into config.ini")
		}
		e.asAdmin(t, 0, "", "instance", "set", id, "OIDC_CLIENT_ID=")
	})

	t.Run("stop and start", func(t *testing.T) {
		e.asAdmin(t, 0, "", "instance", "stop", id)
		if sharkord.Healthz(context.Background(), 5000) == nil {
			t.Fatal("still answering after stop")
		}
		e.asAdmin(t, 0, "", "instance", "start", id)
		e.asAdmin(t, 0, "", "instance", "restart", "--all")
		if err := sharkord.Healthz(context.Background(), 5000); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("logs", func(t *testing.T) {
		if out := e.stdout(t, "", "instance", "logs", id, "-n", "50"); !strings.Contains(out, "sharkord") && !strings.Contains(out, "Sharkord") {
			t.Fatalf("logs: %s", out)
		}
	})
}
