// Package storage checks and adds storage locations: folders on drives that
// come back after a reboot, with a Linux file system that allows running
// programs (Sharkord runs its voice server from the data folder).
package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

// Mount is one file system, as findmnt reports it.
type Mount struct {
	Target  string `json:"target"`
	Source  string `json:"source"`
	FSType  string `json:"fstype"`
	Options string `json:"options"`
	UUID    string `json:"uuid"`
	Label   string `json:"label,omitempty"`
}

func (m Mount) hasOption(o string) bool {
	return slices.Contains(strings.Split(m.Options, ","), o)
}

// FindMount returns the file system holding path.
func FindMount(ctx context.Context, r run.Runner, path string) (Mount, error) {
	res, err := r.Run(ctx, run.Cmd{Name: "findmnt", Args: []string{"-J", "-o", "TARGET,SOURCE,FSTYPE,OPTIONS,UUID,LABEL", "--target", path}})
	if err != nil {
		return Mount{}, err
	}
	var out struct {
		Filesystems []Mount `json:"filesystems"`
	}
	if err := json.Unmarshal(res.Stdout, &out); err != nil || len(out.Filesystems) == 0 {
		return Mount{}, fmt.Errorf("can't read findmnt's answer for %s", path)
	}
	return out.Filesystems[0], nil
}

// DataFS are the file systems accepted for instance data.
var DataFS = []string{"ext4", "xfs", "btrfs"}

var forbiddenPrefixes = []string{"/tmp", "/var/tmp", "/run", "/media", "/boot", "/proc", "/sys", "/dev"}

// FstabEntry is one line of /etc/fstab.
type FstabEntry struct{ Spec, Target string }

// ReadFstab parses /etc/fstab (or another file).
func ReadFstab(path string) ([]FstabEntry, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []FstabEntry
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		out = append(out, FstabEntry{Spec: f[0], Target: f[1]})
	}
	return out, nil
}

// InFstab reports whether m has an fstab line, by UUID, label or mount point.
func InFstab(entries []FstabEntry, m Mount) bool {
	for _, e := range entries {
		switch {
		case m.UUID != "" && (e.Spec == "UUID="+m.UUID || e.Spec == "/dev/disk/by-uuid/"+m.UUID):
			return true
		case m.Label != "" && (e.Spec == "LABEL="+m.Label || e.Spec == "/dev/disk/by-label/"+m.Label):
			return true
		case e.Target == m.Target:
			return true
		}
	}
	return false
}

// CheckLocation says whether path can hold instance data (forData) or
// backups (any local file system that's in fstab). It returns the drive.
func CheckLocation(ctx context.Context, r run.Runner, p, fstab string, forData bool) (Mount, error) {
	p = path.Clean(p)
	if !path.IsAbs(p) {
		return Mount{}, fmt.Errorf("%s isn't an absolute path", p)
	}
	for _, bad := range forbiddenPrefixes {
		if p == bad || strings.HasPrefix(p, bad+"/") {
			return Mount{}, fmt.Errorf("%s is under %s, which doesn't survive a reboot or isn't for data (desktop auto-mounts under /media vanish when nobody's logged in); mount the drive permanently instead", p, bad)
		}
	}
	// The folder may not exist yet; its drive is the nearest existing parent's.
	existing := p
	for {
		if _, err := os.Stat(existing); err == nil || existing == "/" {
			break
		}
		existing = path.Dir(existing)
	}
	m, err := FindMount(ctx, r, existing)
	if err != nil {
		return Mount{}, err
	}
	if forData {
		if !slices.Contains(DataFS, m.FSType) {
			return m, fmt.Errorf("%s is on %s; server data needs ext4, XFS or Btrfs (FAT, exFAT and NTFS have no Linux owners and permissions, and network shares don't work with Sharkord's database)", p, m.FSType)
		}
		if m.hasOption("noexec") {
			return m, fmt.Errorf("%s is mounted with noexec, but Sharkord runs its voice server from the data folder", p)
		}
	} else if strings.HasPrefix(m.FSType, "nfs") || m.FSType == "cifs" || m.FSType == "smb3" || m.FSType == "fuse.sshfs" {
		// Backups are encrypted files, but a network share must still come
		// back after a reboot; fstab is checked below.
	}
	if m.Target != "/" {
		entries, err := ReadFstab(fstab)
		if err != nil {
			return m, err
		}
		if !InFstab(entries, m) {
			return m, fmt.Errorf("the drive at %s isn't in /etc/fstab, so it may not come back after a reboot; use 'storage mount' to mount it permanently", m.Target)
		}
	}
	return m, nil
}

// BlockedParents lists folders above path that others can't pass through
// (no x bit for others), which would stop instance users reaching their data.
func BlockedParents(dir string) []string {
	var out []string
	for p := path.Dir(path.Clean(dir)); ; p = path.Dir(p) {
		if fi, err := os.Stat(p); err == nil && fi.Mode().Perm()&0o001 == 0 {
			out = append(out, p)
		}
		if p == path.Dir(p) {
			break
		}
	}
	return out
}

// Device is a block device lsblk reports.
type Device struct {
	Path        string   `json:"path"`
	Size        string   `json:"size"`
	Type        string   `json:"type"`
	FSType      string   `json:"fstype"`
	UUID        string   `json:"uuid"`
	Label       string   `json:"label"`
	Mountpoints []string `json:"mountpoints"`
	Children    []Device `json:"children"`
}

// Unmounted lists partitions (or whole disks) formatted ext4, XFS or Btrfs
// with no mount point: drives that can be mounted for Sharkord.
func Unmounted(ctx context.Context, r run.Runner) ([]Device, error) {
	// A drive formatted moments ago may not have its UUID recorded yet.
	_, _ = r.Run(ctx, run.Cmd{Name: "udevadm", Args: []string{"settle", "--timeout=10"}, Timeout: 15 * time.Second})
	res, err := r.Run(ctx, run.Cmd{Name: "lsblk", Args: []string{"-J", "-o", "PATH,SIZE,TYPE,FSTYPE,UUID,LABEL,MOUNTPOINTS"}})
	if err != nil {
		return nil, err
	}
	var out struct {
		Blockdevices []Device `json:"blockdevices"`
	}
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return nil, err
	}
	var found []Device
	var walk func([]Device)
	walk = func(ds []Device) {
		for _, d := range ds {
			mounted := false
			for _, m := range d.Mountpoints {
				mounted = mounted || m != ""
			}
			if !mounted && d.UUID != "" && slices.Contains(DataFS, d.FSType) && (d.Type == "part" || d.Type == "disk" || d.Type == "loop") {
				found = append(found, d)
			}
			walk(d.Children)
		}
	}
	walk(out.Blockdevices)
	return found, nil
}

// Probe reads a device's file system straight from the disk with blkid,
// for when udev hasn't recorded it (lsblk shows nothing for it yet). It
// refuses devices that are mounted or don't hold ext4, XFS or Btrfs.
func Probe(ctx context.Context, r run.Runner, dev string) (Device, error) {
	d := Device{Path: dev, Type: "part"}
	res, err := r.Run(ctx, run.Cmd{Name: "blkid", Args: []string{"-p", "-o", "export", dev}})
	if err != nil {
		return d, fmt.Errorf("%s has no file system blkid can read: %w", dev, err)
	}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch k {
		case "TYPE":
			d.FSType = v
		case "UUID":
			d.UUID = v
		case "LABEL":
			d.Label = v
		}
	}
	if d.UUID == "" || !slices.Contains(DataFS, d.FSType) {
		return d, fmt.Errorf("%s isn't formatted ext4, XFS or Btrfs (found %q)", dev, d.FSType)
	}
	if out, err := r.Run(ctx, run.Cmd{Name: "findmnt", Args: []string{"-n", "--source", dev}}); err == nil && strings.TrimSpace(string(out.Stdout)) != "" {
		return d, fmt.Errorf("%s is already mounted", dev)
	}
	return d, nil
}

// MountPointFor names the mount point for a drive: /srv/sharkord-<label>, or
// the start of its UUID when it has no usable label.
func MountPointFor(d Device) string {
	name := strings.ToLower(d.Label)
	clean := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return -1
	}, name)
	if clean == "" {
		clean = strings.ToLower(strings.ReplaceAll(d.UUID, "-", ""))
		if len(clean) > 8 {
			clean = clean[:8]
		}
	}
	return "/srv/sharkord-" + clean
}

// FstabLine is what MountDrive appends. XFS and Btrfs skip the boot-time
// check, so they end in 0; nofail keeps a missing drive from stopping boot.
func FstabLine(d Device, target string) string {
	pass := "2"
	if d.FSType != "ext4" {
		pass = "0"
	}
	return fmt.Sprintf("# Added by shiver-server-manager\nUUID=%s %s %s defaults,nofail 0 %s\n", d.UUID, target, d.FSType, pass)
}

// MountDrive adds a drive to fstab and mounts it. fstab is backed up first
// and put back if findmnt --verify complains.
func MountDrive(ctx context.Context, r run.Runner, d Device, fstab string) (string, error) {
	target := MountPointFor(d)
	if err := fsutil.MkdirAllMode(target, 0o755); err != nil {
		return "", err
	}
	old, err := os.ReadFile(fstab)
	if err != nil {
		return "", err
	}
	entries, _ := ReadFstab(fstab)
	if !InFstab(entries, Mount{UUID: d.UUID, Target: target}) {
		if err := os.WriteFile(fstab+".shiver-server-manager.bak", old, 0o644); err != nil {
			return "", err
		}
		// fstab may already have problems of its own; only new ones count.
		before, _ := verifyFstab(ctx, r, fstab)
		next := string(old)
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		next += FstabLine(d, target)
		if err := fsutil.WriteFileAtomic(fstab, []byte(next), 0o644, nil); err != nil {
			return "", err
		}
		after, details := verifyFstab(ctx, r, fstab)
		if after > before {
			_ = fsutil.WriteFileAtomic(fstab, old, 0o644, nil)
			return "", fmt.Errorf("findmnt --verify found a problem with the new line, so /etc/fstab was put back:\n%s", details)
		}
		if _, err := r.Run(ctx, run.Cmd{Name: "systemctl", Args: []string{"daemon-reload"}}); err != nil {
			return "", err
		}
	}
	if _, err := r.Run(ctx, run.Cmd{Name: "mount", Args: []string{target}}); err != nil {
		return "", err
	}
	m, err := FindMount(ctx, r, target)
	if err != nil || m.Target != target {
		return "", errors.New("the drive didn't mount at " + target)
	}
	return target, nil
}

var verifySummary = regexp.MustCompile(`(\d+) parse errors?, (\d+) errors?`)

// verifyFstab runs findmnt --verify and returns how many problems (parse
// errors plus errors) it reports, and its details.
func verifyFstab(ctx context.Context, r run.Runner, fstab string) (int, string) {
	res, err := r.Run(ctx, run.Cmd{Name: "findmnt", Args: []string{"--verify", "--tab-file", fstab}})
	out := strings.TrimSpace(string(res.Stdout) + "\n" + string(res.Stderr))
	m := verifySummary.FindStringSubmatch(out)
	if m == nil {
		if err != nil {
			return 1 << 20, out // couldn't tell: treat as a problem
		}
		return 0, out
	}
	p, _ := strconv.Atoi(m[1])
	e, _ := strconv.Atoi(m[2])
	return p + e, out
}
