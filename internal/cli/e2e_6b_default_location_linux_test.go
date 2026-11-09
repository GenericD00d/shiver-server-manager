//go:build linux

package cli

import (
	"os"
	"testing"
)

// TestE2EDefaultLocation: a folder made the default in one step is where
// new servers go; a self-signed server behind a "proxy" never checks DNS.
func TestE2EDefaultLocation(t *testing.T) {
	e := newE2E(t)
	if err := os.MkdirAll("/srv/data", 0o755); err != nil {
		t.Fatal(err)
	}
	e.asAdmin(t, 0, "", "storage", "add", "/srv/data/sharkord", "--default")
	defer func() {
		e.asAdmin(t, 0, "", "storage", "default", "/var/lib/sharkord")
		e.asAdmin(t, 0, "", "storage", "remove", "/srv/data/sharkord")
	}()
	e.asAdmin(t, 0, "", "instance", "create", "--id", "chat-d", "--domain", "chat-d.example.com", "--tls", "selfsigned", "--no-encrypt", "--skip-dns-check")
	if info := e.serverInfo(t, "chat-d"); info["location"] != "/srv/data/sharkord" || info["healthy"] != true {
		t.Fatalf("%v", info)
	}
	if _, err := os.Stat("/srv/data/sharkord/chat-d/db.sqlite"); err != nil {
		t.Fatal(err)
	}
	e.asAdmin(t, 0, "chat-d\n", "instance", "delete", "chat-d")
}
