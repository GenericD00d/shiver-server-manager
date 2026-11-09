package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

var ctx = context.Background()

func findmnt(target, fstype, options, uuid string) string {
	return `{"filesystems": [{"target":"` + target + `","source":"/dev/sdb1","fstype":"` + fstype +
		`","options":"` + options + `","uuid":"` + uuid + `","label":null}]}`
}

func fstabFile(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "fstab")
	os.WriteFile(p, []byte(body), 0o644)
	return p
}

func TestCheckLocation(t *testing.T) {
	fstab := fstabFile(t, "UUID=root-uuid / ext4 defaults 0 1\nUUID=6f1c / ext4 defaults 0 2\n# UUID=commented /x ext4\n/dev/sdc1 /srv/byname xfs defaults 0 0\n")
	cases := []struct {
		name, path, out string
		forData, ok     bool
	}{
		{"root fs", "/var/lib/sharkord", findmnt("/", "ext4", "rw,relatime", "root-uuid"), true, true},
		{"second disk in fstab", "/srv/disk2/sharkord", findmnt("/srv/disk2", "ext4", "rw", "6f1c"), true, true},
		{"by mount point", "/srv/byname/x", findmnt("/srv/byname", "xfs", "rw", "zz"), true, true},
		{"not in fstab", "/srv/usb/x", findmnt("/srv/usb", "ext4", "rw", "nope"), true, false},
		{"FAT", "/srv/fat/x", findmnt("/srv/fat", "vfat", "rw", "6f1c"), true, false},
		{"NTFS", "/srv/ntfs/x", findmnt("/srv/ntfs", "ntfs3", "rw", "6f1c"), true, false},
		{"noexec", "/srv/disk2/x", findmnt("/srv/disk2", "ext4", "rw,nosuid,noexec", "6f1c"), true, false},
		{"FAT is fine for backups", "/srv/fat/x", findmnt("/srv/fat", "vfat", "rw", "6f1c"), false, true},
		{"media", "/media/user/disk", findmnt("/media/user/disk", "ext4", "rw", "6f1c"), true, false},
		{"tmp", "/tmp/x", findmnt("/", "ext4", "rw", "root-uuid"), true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := (&run.Fake{}).On("findmnt", run.FakeResponse{Stdout: c.out})
			_, err := CheckLocation(ctx, f, c.path, fstab, c.forData)
			if (err == nil) != c.ok {
				t.Fatalf("got %v, want ok=%v", err, c.ok)
			}
		})
	}
}

const lsblk = `{"blockdevices": [
  {"path":"/dev/sda","size":"40G","type":"disk","fstype":null,"uuid":null,"label":null,"mountpoints":[null],
   "children":[{"path":"/dev/sda1","size":"40G","type":"part","fstype":"ext4","uuid":"root","label":null,"mountpoints":["/"]}]},
  {"path":"/dev/sdb","size":"100G","type":"disk","fstype":null,"uuid":null,"label":null,"mountpoints":[null],
   "children":[{"path":"/dev/sdb1","size":"100G","type":"part","fstype":"ext4","uuid":"6f1c2a9e-3b7d-4c55-9e0a-2d4f8b1c7e63","label":"Disk 2!","mountpoints":[null]}]},
  {"path":"/dev/sdc1","size":"8G","type":"part","fstype":"vfat","uuid":"ABCD-1234","label":"USB","mountpoints":[null]}
]}`

func TestUnmounted(t *testing.T) {
	f := (&run.Fake{}).On("lsblk", run.FakeResponse{Stdout: lsblk})
	ds, err := Unmounted(ctx, f)
	if err != nil || len(ds) != 1 || ds[0].Path != "/dev/sdb1" {
		t.Fatalf("%+v %v", ds, err)
	}
	if MountPointFor(ds[0]) != "/srv/sharkord-disk2" {
		t.Fatalf("mount point %s", MountPointFor(ds[0]))
	}
	if MountPointFor(Device{UUID: "6f1c2a9e-3b7d"}) != "/srv/sharkord-6f1c2a9e" {
		t.Fatal("UUID mount point")
	}
	if got := FstabLine(ds[0], "/srv/sharkord-disk2"); !strings.HasSuffix(got, "UUID=6f1c2a9e-3b7d-4c55-9e0a-2d4f8b1c7e63 /srv/sharkord-disk2 ext4 defaults,nofail 0 2\n") {
		t.Fatalf("fstab line %q", got)
	}
	if got := FstabLine(Device{UUID: "u", FSType: "xfs"}, "/x"); !strings.HasSuffix(got, " 0 0\n") {
		t.Fatalf("xfs line %q", got)
	}
}

func TestMountDriveRestoresFstabOnVerifyFailure(t *testing.T) {
	if os.Getenv("SSM_ROOT_TESTS") == "" {
		t.Skip("creates /srv/sharkord-*; set SSM_ROOT_TESTS on a throwaway machine")
	}
	fstab := fstabFile(t, "UUID=root / ext4 defaults 0 1\n")
	calls := 0
	f := &run.Fake{Handler: func(c run.Call) (run.FakeResponse, bool) {
		calls++
		if calls == 1 { // before: the admin's fstab already has one error
			return run.FakeResponse{ExitCode: 1, Stderr: "0 parse errors, 1 error, 0 warnings"}, true
		}
		return run.FakeResponse{ExitCode: 1, Stderr: "1 parse error, 1 error, 0 warnings"}, true
	}}
	_, err := MountDrive(ctx, f, Device{UUID: "u", FSType: "ext4", Label: "t"}, fstab)
	if err == nil {
		t.Fatal("verify failure not reported")
	}
	if b, _ := os.ReadFile(fstab); string(b) != "UUID=root / ext4 defaults 0 1\n" {
		t.Fatalf("fstab not restored:\n%s", b)
	}
}

func TestProbe(t *testing.T) {
	f := (&run.Fake{}).On("blkid -p -o export /dev/loop3", run.FakeResponse{Stdout: "DEVNAME=/dev/loop3\nUUID=abcd-1234\nTYPE=ext4\nLABEL=disk2\n"}).
		On("findmnt", run.FakeResponse{ExitCode: 1})
	d, err := Probe(ctx, f, "/dev/loop3")
	if err != nil || d.UUID != "abcd-1234" || d.FSType != "ext4" || MountPointFor(d) != "/srv/sharkord-disk2" {
		t.Fatalf("%+v %v", d, err)
	}
	f = (&run.Fake{}).On("blkid", run.FakeResponse{Stdout: "UUID=1234-ABCD\nTYPE=vfat\n"}).On("findmnt", run.FakeResponse{ExitCode: 1})
	if _, err := Probe(ctx, f, "/dev/sdc1"); err == nil {
		t.Fatal("FAT accepted")
	}
	f = (&run.Fake{}).On("blkid", run.FakeResponse{Stdout: "UUID=u\nTYPE=xfs\n"}).On("findmnt", run.FakeResponse{Stdout: "/srv/x /dev/sdd1 xfs rw\n"})
	if _, err := Probe(ctx, f, "/dev/sdd1"); err == nil {
		t.Fatal("a mounted drive accepted")
	}
}
