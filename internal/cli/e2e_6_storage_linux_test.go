//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
)

func sh(t *testing.T, cmd string) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", cmd, err, out)
	}
	return strings.TrimSpace(string(out))
}

// serverInfo reads one server's record and live state.
func (e *e2e) serverInfo(t *testing.T, id string) map[string]any {
	t.Helper()
	var info map[string]any
	if err := json.Unmarshal([]byte(e.stdout(t, "", "instance", "show", id, "--json")), &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func (e *e2e) port(t *testing.T, id string) int {
	return int(e.serverInfo(t, id)["httpPort"].(float64))
}

// TestE2EEncryptedAndStorage: milestone 7.
func TestE2EEncryptedAndStorage(t *testing.T) {
	e := newE2E(t)
	ctx := context.Background()
	const enc = "chat-e"

	t.Run("encrypted by default", func(t *testing.T) {
		e.asAdmin(t, 0, "", "instance", "create", "--id", enc, "--domain", "chat-e.example.com", "--tls", "selfsigned")
		info := e.serverInfo(t, enc)
		if info["encrypted"] != true || info["healthy"] != true {
			t.Fatalf("%v", info)
		}
		if out := sh(t, "findmnt -n -o SOURCE,OPTIONS --mountpoint /var/lib/sharkord/"+enc); !strings.Contains(out, "/dev/mapper/sharkord-"+enc) || !strings.Contains(out, "nodev") || strings.Contains(out, "noexec") {
			t.Fatalf("mount: %s", out)
		}
		if _, err := os.Stat("/var/lib/sharkord/" + enc + ".luks"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("after a reboot it stays stopped until the vault is unlocked", func(t *testing.T) {
		// What a reboot leaves: service down, container closed.
		sh(t, "systemctl stop sharkord@"+enc+" && umount /var/lib/sharkord/"+enc+" && cryptsetup close sharkord-"+enc)
		_ = exec.Command("systemctl", "start", "sharkord@"+enc).Run() // what boot would try
		if st := sh(t, "systemctl show -p ActiveState --value sharkord@"+enc); st == "active" {
			t.Fatal("started with its container locked")
		}
		if entries, _ := os.ReadDir("/var/lib/sharkord/" + enc); len(entries) != 0 {
			t.Fatal("Sharkord wrote into the bare mount point")
		}
		if info := e.serverInfo(t, enc); info["locked"] != true {
			t.Fatalf("not shown as locked: %v", info)
		}
		e.asAdmin(t, 0, e2ePass2+"\n", "vault", "unlock")
		if err := sharkord.Healthz(ctx, e.port(t, enc)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("grow", func(t *testing.T) {
		e.asAdmin(t, 0, e2ePass2+"\n", "instance", "grow", enc, "--size", "2")
		size := sh(t, "stat -c %s /var/lib/sharkord/"+enc+".luks")
		if size != "2147483648" {
			t.Fatalf("container size %s", size)
		}
		var mib int
		fmt.Sscanf(sh(t, "df -BM --output=size /var/lib/sharkord/"+enc+" | tail -1 | tr -d ' M'"), "%d", &mib)
		if mib < 1500 {
			t.Fatalf("file system not grown: %d MiB", mib)
		}
		if err := sharkord.Healthz(ctx, e.port(t, enc)); err != nil {
			t.Fatal(err)
		}
	})

	var loop string
	t.Run("second disk through storage mount", func(t *testing.T) {
		sh(t, "truncate -s 6G /var/tmp/disk2.img")
		loop = sh(t, "losetup -f --show /var/tmp/disk2.img")
		sh(t, "mkfs.ext4 -q -L disk2 "+loop)
		e.asAdmin(t, 0, "", "storage", "mount", loop, "--yes")
		if out := sh(t, "grep disk2 /etc/fstab || grep sharkord-disk2 /etc/fstab"); !strings.Contains(out, "nofail") {
			t.Fatalf("fstab: %s", out)
		}
		if out := e.stdout(t, "", "storage", "list"); !strings.Contains(out, "/srv/sharkord-disk2/sharkord") {
			t.Fatalf("storage list:\n%s", out)
		}
	})

	t.Run("move a plain server and an encrypted one onto it", func(t *testing.T) {
		for _, id := range []string{"chat-a", enc} {
			e.asAdmin(t, 0, e2ePass2+"\n", "instance", "move", id, "--to", "/srv/sharkord-disk2/sharkord", "--yes")
			info := e.serverInfo(t, id)
			if info["location"] != "/srv/sharkord-disk2/sharkord" || info["healthy"] != true {
				t.Fatalf("%s after move: %v", id, info)
			}
			if _, err := os.Stat("/var/lib/sharkord/" + id); err == nil {
				t.Fatalf("%s: old copy not deleted", id)
			}
		}
		token := strings.TrimSpace(e.stdout(t, e2ePass2+"\n", "token", "show", "chat-a"))
		if err := sharkord.CheckToken("/srv/sharkord-disk2/sharkord/chat-a", token); err != nil {
			t.Fatalf("data didn't survive the move: %v", err)
		}
	})

	t.Run("FAT, noexec and unlisted drives are refused", func(t *testing.T) {
		sh(t, "truncate -s 64M /var/tmp/fat.img && mkfs.vfat /var/tmp/fat.img >/dev/null && mkdir -p /srv/fat && mount -o loop /var/tmp/fat.img /srv/fat")
		sh(t, "truncate -s 64M /var/tmp/noexec.img && mkfs.ext4 -q /var/tmp/noexec.img && mkdir -p /srv/noexec && mount -o loop,noexec /var/tmp/noexec.img /srv/noexec")
		sh(t, "echo '/var/tmp/noexec.img /srv/noexec ext4 loop,noexec,nofail 0 0' >> /etc/fstab")
		for _, p := range []string{"/srv/fat/sharkord", "/srv/noexec/sharkord", "/media/runner/usb/sharkord"} {
			out := e.asAdmin(t, ExitError, "", "storage", "add", p)
			if !strings.Contains(out, "vfat") && !strings.Contains(out, "noexec") && !strings.Contains(out, "/media") {
				t.Fatalf("%s refused for the wrong reason: %s", p, out)
			}
		}
		sh(t, "umount /srv/fat /srv/noexec")
	})

	t.Run("a missing drive only stops its own servers", func(t *testing.T) {
		sh(t, "systemctl stop sharkord@chat-a sharkord@"+enc)
		sh(t, "umount /srv/sharkord-disk2/sharkord/"+enc+"; cryptsetup close sharkord-"+enc+"; umount /srv/sharkord-disk2 && losetup -d "+loop)
		_ = exec.Command("systemctl", "start", "--no-block", "sharkord@chat-a").Run()
		if sharkord.Healthz(ctx, e.port(t, "chat-a")) == nil {
			t.Fatal("started without its drive")
		}
		if err := sharkord.Healthz(ctx, e.port(t, "chat-b")); err != nil {
			t.Fatal("a server on another drive went down")
		}
		if out := e.stdout(t, "", "storage", "list"); !strings.Contains(out, "!") {
			t.Logf("storage list (expected a problem shown):\n%s", out)
		}
		// Put it back.
		// udev must have seen the drive come back first, or systemd takes the
		// mount straight down again (openSUSE).
		sh(t, "losetup "+loop+" /var/tmp/disk2.img && udevadm settle && mount /srv/sharkord-disk2")
		if _, err := os.Stat("/srv/sharkord-disk2/sharkord/" + enc + ".luks"); err != nil {
			diag, _ := exec.Command("sh", "-c", "findmnt /srv/sharkord-disk2; ls -la /srv/sharkord-disk2 /srv/sharkord-disk2/sharkord; journalctl --no-pager -n 40").CombinedOutput()
			t.Fatalf("the drive's container is missing after remounting it: %v\n%s", err, diag)
		}
		e.asAdmin(t, 0, e2ePass2+"\n", "vault", "unlock")
		e.asAdmin(t, 0, "", "instance", "start", "chat-a")
	})

	t.Run("delete erases the container", func(t *testing.T) {
		e.asAdmin(t, 0, enc+"\n", "instance", "delete", enc)
		if _, err := os.Stat("/srv/sharkord-disk2/sharkord/" + enc + ".luks"); err == nil {
			t.Fatal("container file left")
		}
		if _, err := os.Stat("/dev/mapper/sharkord-" + enc); err == nil {
			t.Fatal("container still open")
		}
	})
}
