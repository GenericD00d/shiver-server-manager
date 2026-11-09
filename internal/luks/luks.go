// Package luks manages each encrypted instance's container: a LUKS2 file
// holding an ext4 file system, mounted on the instance's data folder.
// Keys are 64 random bytes and always reach cryptsetup on stdin.
package luks

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

// KeySize is the container key's length in bytes.
const KeySize = 64

// NewKey makes a container key.
func NewKey() ([]byte, error) {
	k := make([]byte, KeySize)
	_, err := rand.Read(k)
	return k, err
}

// Mapper is the device-mapper name of an instance's open container.
func Mapper(id string) string { return "sharkord-" + id }

// Device is the open container's block device.
func Device(id string) string { return "/dev/mapper/" + Mapper(id) }

type LUKS struct{ R run.Runner }

func (l LUKS) run(ctx context.Context, name string, key []byte, args ...string) (run.Result, error) {
	c := run.Cmd{Name: name, Args: args, Timeout: 10 * time.Minute}
	if key != nil {
		c.Stdin = bytes.NewReader(key)
		c.Secrets = [][]byte{key}
	}
	return l.R.Run(ctx, c)
}

// Allocate creates the container file at size. On Btrfs the file is created
// empty and marked no-copy-on-write first: copy-on-write fragments a big
// file rewritten in place, and the flag only takes effect on an empty file.
func (l LUKS) Allocate(ctx context.Context, container string, size int64, btrfs bool) error {
	if btrfs {
		f, err := os.OpenFile(container, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		f.Close()
		if _, err := l.run(ctx, "chattr", nil, "+C", container); err != nil {
			return err
		}
	}
	if _, err := l.run(ctx, "fallocate", nil, "-l", strconv.FormatInt(size, 10), container); err != nil {
		return err
	}
	return os.Chmod(container, 0o600)
}

// IsLUKS reports whether the file has a LUKS header.
func (l LUKS) IsLUKS(ctx context.Context, container string) bool {
	_, err := l.run(ctx, "cryptsetup", nil, "isLuks", container)
	return err == nil
}

func (l LUKS) Format(ctx context.Context, container string, key []byte) error {
	_, err := l.run(ctx, "cryptsetup", key, "luksFormat", "--type", "luks2", "--batch-mode",
		"--key-file=-", "--keyfile-size="+strconv.Itoa(KeySize), container)
	return err
}

// IsOpen reports whether the container's mapping exists.
func IsOpen(id string) bool {
	_, err := os.Stat(Device(id))
	return err == nil
}

func (l LUKS) Open(ctx context.Context, container, id string, key []byte) error {
	if IsOpen(id) {
		return nil
	}
	_, err := l.run(ctx, "cryptsetup", key, "open", "--type", "luks2",
		"--key-file=-", "--keyfile-size="+strconv.Itoa(KeySize), container, Mapper(id))
	return err
}

func (l LUKS) Close(ctx context.Context, id string) error {
	if !IsOpen(id) {
		return nil
	}
	_, err := l.run(ctx, "cryptsetup", nil, "close", Mapper(id))
	return err
}

// HasFilesystem asks blkid whether the open container is formatted
// (exit code 2 means nothing found).
func (l LUKS) HasFilesystem(ctx context.Context, id string) (bool, error) {
	_, err := l.run(ctx, "blkid", nil, "-p", Device(id))
	var exitErr *run.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode == 2 {
		return false, nil
	}
	return err == nil, err
}

// Mkfs makes the ext4 file system. ext4 labels hold 16 bytes, too few for
// "sharkord-<id>" with a long ID, so the label is shortened.
func (l LUKS) Mkfs(ctx context.Context, id string) error {
	label := "sharkord-" + id
	if len(label) > 16 {
		label = label[:16]
	}
	_, err := l.run(ctx, "mkfs.ext4", nil, "-q", "-L", label, Device(id))
	return err
}

// Fsck runs e2fsck -p. Exit codes 0 and 1 (errors fixed) are fine; 4 or
// higher means stop and report.
func (l LUKS) Fsck(ctx context.Context, id string) error {
	_, err := l.run(ctx, "e2fsck", nil, "-p", Device(id))
	var exitErr *run.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode < 4 {
		return nil
	}
	if err != nil {
		return fmt.Errorf("the file system check found problems it couldn't fix; run 'e2fsck %s' by hand: %w", Device(id), err)
	}
	return nil
}

// IsMounted reports whether dir is a mount point.
func (l LUKS) IsMounted(ctx context.Context, dir string) bool {
	res, err := l.run(ctx, "findmnt", nil, "-n", "-o", "TARGET", "--mountpoint", dir)
	return err == nil && strings.TrimSpace(string(res.Stdout)) == dir
}

// Mount mounts the container on dir. Never noexec: Sharkord runs its voice
// server from the data folder.
func (l LUKS) Mount(ctx context.Context, id, dir string) error {
	if l.IsMounted(ctx, dir) {
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_, err := l.run(ctx, "mount", nil, "-o", "nodev,nosuid", Device(id), dir)
	return err
}

func (l LUKS) Umount(ctx context.Context, dir string) error {
	if !l.IsMounted(ctx, dir) {
		return nil
	}
	_, err := l.run(ctx, "umount", nil, dir)
	return err
}

// HeaderBackup saves the LUKS header to a file. A damaged header makes the
// data unreadable even with the key.
func (l LUKS) HeaderBackup(ctx context.Context, container, file string) error {
	_ = os.Remove(file)
	_, err := l.run(ctx, "cryptsetup", nil, "luksHeaderBackup", container, "--header-backup-file", file)
	return err
}

// Resize grows the open mapping to the container file's new size.
func (l LUKS) Resize(ctx context.Context, id string, key []byte) error {
	_, err := l.run(ctx, "cryptsetup", key, "resize", "--key-file=-", "--keyfile-size="+strconv.Itoa(KeySize), Mapper(id))
	return err
}

func (l LUKS) Resize2fs(ctx context.Context, id string) error {
	_, err := l.run(ctx, "resize2fs", nil, Device(id))
	return err
}

// Erase wipes every key slot. Afterwards nothing on the disk can be
// decrypted, even with the key.
func (l LUKS) Erase(ctx context.Context, container string) error {
	if _, err := os.Stat(container); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if !l.IsLUKS(ctx, container) {
		return nil
	}
	_, err := l.run(ctx, "cryptsetup", nil, "erase", "--batch-mode", container)
	return err
}
