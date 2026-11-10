//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
)

// TestE2EShiver: milestone 9. One command installs the real Shiver plugin on
// a fresh server, switches plugins on, and Sharkord loads it.
func TestE2EShiver(t *testing.T) {
	e := newE2E(t)
	const id = "chat-a"
	port := e.port(t, id)

	t.Run("install", func(t *testing.T) {
		before := time.Now()
		e.asAdmin(t, 0, "", "shiver", "install", id)
		journal, _ := exec.Command("journalctl", "-u", "sharkord@"+id, "--since", before.Format("2006-01-02 15:04:05"), "-o", "cat", "--no-pager").CombinedOutput()
		if !strings.Contains(string(journal), "Plugin loaded: shiver@v") {
			t.Fatalf("Sharkord didn't log Shiver as loaded:\n%s", journal)
		}
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/plugin-bundle/shiver/client/index.js", port))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("plugin bundle: %s (403 means plugins are off, 404 Shiver is)", resp.Status)
		}
		if err := sharkord.Healthz(context.Background(), port); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("status and update", func(t *testing.T) {
		out := e.stdout(t, "", "shiver", "status")
		if !strings.Contains(out, id) || !strings.Contains(out, "on") {
			t.Fatalf("status:\n%s", out)
		}
		// Already the latest: update says so and changes nothing.
		out = e.asAdmin(t, ExitError, "", "shiver", "update", "--all")
		if !strings.Contains(out, "already has Shiver") {
			t.Fatalf("update --all:\n%s", out)
		}
	})

	t.Run("a plugin from a tar.gz file", func(t *testing.T) {
		// Shiver again, packed as a tarball, through the generic install.
		data := e.serverInfo(t, id)["dataPath"].(string)
		archive := "/var/tmp/shiver-plugin.tar.gz"
		if out, err := exec.Command("tar", "-czf", archive, "-C", data+"/plugins", "shiver").CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		before := time.Now()
		e.asAdmin(t, 0, "", "plugin", "install", id, archive)
		journal, _ := exec.Command("journalctl", "-u", "sharkord@"+id, "--since", before.Format("2006-01-02 15:04:05"), "-o", "cat", "--no-pager").CombinedOutput()
		if !strings.Contains(string(journal), "Plugin loaded: shiver@v") {
			t.Fatalf("Sharkord didn't load the plugin:\n%s", journal)
		}
		var lists []struct {
			Instance string
			Plugins  []struct{ ID, Version string }
		}
		if err := json.Unmarshal([]byte(e.stdout(t, "", "plugin", "list", id, "--json")), &lists); err != nil || len(lists) != 1 || len(lists[0].Plugins) != 1 || lists[0].Plugins[0].ID != "shiver" {
			t.Fatalf("%+v %v", lists, err)
		}
		// Not a plugin: refused, and the server keeps running.
		exec.Command("sh", "-c", "echo hi > /var/tmp/notes.txt && tar -czf /var/tmp/not-a-plugin.tar.gz -C /var/tmp notes.txt").Run()
		e.asAdmin(t, ExitError, "", "plugin", "install", id, "/var/tmp/not-a-plugin.tar.gz")
		if err := sharkord.Healthz(context.Background(), port); err != nil {
			t.Fatal(err)
		}
	})
}
