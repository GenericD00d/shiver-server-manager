//go:build linux

package cli

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
)

// killAfter runs the binary as the admin and SIGKILLs it as soon as a line
// of its output contains marker, like a crash or a lost SSH session.
func (e *e2e) killAfter(t *testing.T, marker, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(e.bin, append(pinned(args), "--password-stdin", "--passphrase-stdin")...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "SUDO_USER=" + e.admin}
	cmd.Stdin = strings.NewReader(e.pw + "\n" + stdin)
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var seen strings.Builder
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		seen.WriteString(sc.Text() + "\n")
		if strings.Contains(sc.Text(), marker) {
			_ = cmd.Process.Kill()
			break
		}
	}
	_ = cmd.Wait()
	if !strings.Contains(seen.String(), marker) {
		t.Fatalf("never saw %q:\n%s", marker, seen.String())
	}
	return seen.String()
}

// TestE2ERepair: milestone 5. Killing the manager mid-create and mid-delete,
// then running repair, leaves nothing half done.
func TestE2ERepair(t *testing.T) {
	e := newE2E(t)
	const id = "chat-k"

	t.Run("killed create is finished by repair", func(t *testing.T) {
		e.killAfter(t, "… first-start", "", "instance", "create", "--id", id, "--domain", "chat-k.example.com", "--no-encrypt")
		out := e.stdout(t, "", "instance", "show", id)
		if !strings.Contains(out, "Unfinished") {
			t.Fatalf("no unfinished operation recorded:\n%s", out)
		}
		e.asAdmin(t, 0, "", "repair", "--yes")
		if err := sharkord.Healthz(context.Background(), 5001); err != nil {
			t.Fatalf("not answering after repair: %v", err)
		}
		if out := e.stdout(t, "", "repair", "--dry-run"); !strings.Contains(out, "Everything matches") {
			t.Fatalf("repair left differences:\n%s", out)
		}
	})

	t.Run("repair restarts a crashed server but not a stopped one", func(t *testing.T) {
		_ = exec.Command("systemctl", "kill", "-s", "KILL", "sharkord@"+id).Run()
		_ = exec.Command("systemctl", "stop", "sharkord@"+id).Run() // as if it stayed down
		e.asAdmin(t, 0, "", "repair", "--yes")
		if err := sharkord.Healthz(context.Background(), 5001); err != nil {
			t.Fatal(err)
		}
		e.asAdmin(t, 0, "", "instance", "stop", id)
		e.asAdmin(t, 0, "", "repair", "--yes")
		if sharkord.Healthz(context.Background(), 5001) == nil {
			t.Fatal("repair started a server an admin stopped")
		}
		e.asAdmin(t, 0, "", "instance", "start", id)
	})

	t.Run("delete needs the ID typed back", func(t *testing.T) {
		e.asAdmin(t, ExitError, "wrong-id\n", "instance", "delete", id)
		if err := sharkord.Healthz(context.Background(), 5001); err != nil {
			t.Fatal("a refused delete stopped the server")
		}
	})

	t.Run("killed delete is finished by repair", func(t *testing.T) {
		e.killAfter(t, "✓ firewall", id+"\n", "instance", "delete", id)
		e.asAdmin(t, 0, "", "repair", "--yes")
		if ok, _ := osuser.UserExists(context.Background(), runnerForTest(), osuser.InstanceUser(id)); ok {
			t.Fatal("the system user is still there")
		}
		for _, p := range []string{"/var/lib/sharkord/" + id, "/etc/shiver-server-manager/instances/" + id + ".env",
			"/etc/caddy/sites/" + id + ".caddy", "/etc/systemd/system/sharkord@" + id + ".service.d"} {
			if _, err := os.Lstat(p); err == nil {
				t.Errorf("%s is still there", p)
			}
		}
		if out := e.stdout(t, "", "instance", "list"); strings.Contains(out, id) {
			t.Fatalf("still listed:\n%s", out)
		}
		if _, fw := firewallText(t); strings.Contains(fw, "sharkord:"+id) || strings.Contains(fw, "sharkord-"+id) {
			t.Fatal("firewall rules left")
		}
	})

	t.Run("repair offers leftovers but keeps data without a yes", func(t *testing.T) {
		os.MkdirAll("/var/lib/sharkord/ghost", 0o700)
		os.WriteFile("/etc/shiver-server-manager/instances/ghost.env", []byte("# Managed by shiver-server-manager (instance ghost). Do not edit.\n"), 0o600)
		out := e.stdout(t, "", "repair", "--dry-run")
		if !strings.Contains(out, "ghost") {
			t.Fatalf("leftovers not found:\n%s", out)
		}
		e.asAdmin(t, 0, "", "repair", "--yes")
		if _, err := os.Stat("/etc/shiver-server-manager/instances/ghost.env"); err == nil {
			t.Fatal("leftover env file kept")
		}
		if _, err := os.Stat("/var/lib/sharkord/ghost"); err != nil {
			t.Fatal("a data folder was removed without an explicit yes")
		}
		os.RemoveAll("/var/lib/sharkord/ghost")
	})
	time.Sleep(time.Second)
}
