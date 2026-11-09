package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/luks"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/storage"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// ErrVaultLocked means an operation needs a container key.
var ErrVaultLocked = errors.New("this needs the vault unlocked (the encrypted data folders' keys are in it)")

const gib = int64(1) << 30

// ContainerPath is an encrypted instance's container file.
func ContainerPath(inst *state.Instance, id string) string {
	return filepath.Join(inst.Location, id+".luks")
}

func (s *Sys) luksKey(id string) ([]byte, error) {
	if s.VaultKey == nil {
		return nil, ErrVaultLocked
	}
	return s.Vault.GetSecret(s.VaultKey, id, vault.LUKSKey)
}

func (s *Sys) isBtrfs(ctx context.Context, dir string) bool {
	m, err := storage.FindMount(ctx, s.R, dir)
	return err == nil && m.FSType == "btrfs"
}

// stepContainer creates the LUKS container. The key is saved to the vault
// before formatting, so a crash can never leave a container whose key is
// lost; the header backup goes into the vault too.
func (s *Sys) stepContainer(id string) Step {
	inst := s.St.Instances[id]
	c := ContainerPath(inst, id)
	return Step{
		Name: "container",
		Done: func(ctx context.Context) (bool, error) {
			return fsutil.Exists(c) && s.Vault.HasSecret(id, vault.LUKSKey) && s.Vault.HasSecret(id, vault.LUKSHeader) && s.LUKS.IsLUKS(ctx, c), nil
		},
		Do: func(ctx context.Context) error {
			var key []byte
			var err error
			if s.Vault.HasSecret(id, vault.LUKSKey) {
				if key, err = s.luksKey(id); err != nil {
					return err
				}
			} else {
				if key, err = luks.NewKey(); err != nil {
					return err
				}
				if err := s.Vault.PutSecret(id, vault.LUKSKey, key); err != nil {
					return err
				}
				// Kept for the mount step of this same run, so a create
				// doesn't need the vault unlocked.
				if s.newKeys == nil {
					s.newKeys = map[string][]byte{}
				}
				s.newKeys[id] = append([]byte{}, key...)
			}
			defer clear(key)
			if !s.LUKS.IsLUKS(ctx, c) {
				size := int64(s.St.Settings.ContainerSizeGiB) * gib
				if err := s.LUKS.Allocate(ctx, c, size, s.isBtrfs(ctx, inst.Location)); err != nil {
					return err
				}
				if err := s.LUKS.Format(ctx, c, key); err != nil {
					return err
				}
			}
			tmp := filepath.Join(s.Paths.VaultDir, ".header-"+id)
			defer os.Remove(tmp)
			if err := s.LUKS.HeaderBackup(ctx, c, tmp); err != nil {
				return err
			}
			hdr, err := os.ReadFile(tmp)
			if err != nil {
				return err
			}
			return s.Vault.PutSecret(id, vault.LUKSHeader, hdr)
		},
	}
}

// keyFor returns the container key, from this run's cache or the vault.
func (s *Sys) keyFor(id string) ([]byte, error) {
	if k := s.newKeys[id]; k != nil {
		return append([]byte{}, k...), nil
	}
	return s.luksKey(id)
}

// openAndMount opens the container, checks it and mounts it. With
// format=true an empty container gets its file system first.
func (s *Sys) openAndMount(ctx context.Context, id string, format bool) error {
	inst := s.St.Instances[id]
	if s.LUKS.IsMounted(ctx, inst.DataPath) {
		return nil
	}
	key, err := s.keyFor(id)
	if err != nil {
		return err
	}
	defer clear(key)
	if err := s.LUKS.Open(ctx, ContainerPath(inst, id), id, key); err != nil {
		return err
	}
	has, err := s.LUKS.HasFilesystem(ctx, id)
	if err != nil {
		return err
	}
	if !has {
		if !format {
			return fmt.Errorf("the container for %s has no file system; it may be damaged", id)
		}
		if err := s.LUKS.Mkfs(ctx, id); err != nil {
			return err
		}
	}
	if err := s.LUKS.Fsck(ctx, id); err != nil {
		return err
	}
	return s.LUKS.Mount(ctx, id, inst.DataPath)
}

func (s *Sys) stepMount(id string) Step {
	inst := s.St.Instances[id]
	return Step{
		Name: "mount",
		Done: func(ctx context.Context) (bool, error) { return s.LUKS.IsMounted(ctx, inst.DataPath), nil },
		Do:   func(ctx context.Context) error { return s.openAndMount(ctx, id, true) },
	}
}

// Lock stops an encrypted instance, unmounts and closes its container.
func (s *Sys) Lock(ctx context.Context, id string) error {
	inst, err := s.Instance(id)
	if err != nil {
		return err
	}
	if err := s.Systemd.Stop(ctx, systemd.Unit(id)); err != nil {
		return err
	}
	if err := s.LUKS.Umount(ctx, inst.DataPath); err != nil {
		return err
	}
	return s.LUKS.Close(ctx, id)
}

// eraseContainer is the delete step for an encrypted instance: lock, wipe
// every key slot, delete the file. Nothing on the disk can be decrypted
// afterwards.
func (s *Sys) eraseContainer(ctx context.Context, id string) error {
	inst := s.St.Instances[id]
	if err := s.Lock(ctx, id); err != nil {
		return err
	}
	c := ContainerPath(inst, id)
	if err := s.LUKS.Erase(ctx, c); err != nil {
		return err
	}
	return fsutil.RemoveIfExists(c)
}

// UnlockResult says what happened to one encrypted instance.
type UnlockResult struct {
	ID  string
	Err error
}

// Locked lists encrypted instances whose container isn't mounted.
func (s *Sys) Locked(ctx context.Context) []string {
	var out []string
	for _, id := range s.IDs() {
		inst := s.St.Instances[id]
		if inst.Encrypted && inst.Operation == nil && !s.LUKS.IsMounted(ctx, inst.DataPath) {
			out = append(out, id)
		}
	}
	return out
}

// UnlockAll opens, checks and mounts every locked encrypted instance, then
// starts it (unless an admin stopped it). Needs VaultKey.
func (s *Sys) UnlockAll(ctx context.Context) []UnlockResult {
	var out []UnlockResult
	for _, id := range s.Locked(ctx) {
		err := s.openAndMount(ctx, id, false)
		if err == nil && !s.St.Instances[id].Stopped {
			if err = s.Systemd.Start(ctx, systemd.Unit(id)); err == nil {
				err = s.WaitHealthy(ctx, s.St.Instances[id].HTTPPort, 90*time.Second)
			}
		}
		out = append(out, UnlockResult{ID: id, Err: err})
	}
	return out
}

// Grow makes an encrypted instance's container bigger. It's grown closed,
// so the loop device cryptsetup makes picks up the new size, then the file
// system is grown online.
func (s *Sys) Grow(ctx context.Context, id string, newGiB int) error {
	inst, err := s.requireActive(id)
	if err != nil {
		return err
	}
	if !inst.Encrypted {
		return fmt.Errorf("%s isn't in an encrypted container; its data can use all the free space on its drive", id)
	}
	c := ContainerPath(inst, id)
	fi, err := os.Stat(c)
	if err != nil {
		return err
	}
	newSize := int64(newGiB) * gib
	if newSize <= fi.Size() {
		return fmt.Errorf("the container is already %d GiB; give a bigger size", fi.Size()/gib)
	}
	if free, err := fsutil.FreeBytes(inst.Location); err == nil && int64(free) < newSize-fi.Size()+gib {
		return fmt.Errorf("not enough free space at %s: growing needs %d GiB more, plus 1 GiB spare", inst.Location, (newSize-fi.Size())/gib)
	}
	key, err := s.luksKey(id)
	if err != nil {
		return err
	}
	defer clear(key)
	if err := s.Lock(ctx, id); err != nil {
		return err
	}
	if _, err := s.R.Run(ctx, runCmd("fallocate", "-l", fmt.Sprint(newSize), c)); err != nil {
		return err
	}
	if err := s.LUKS.Open(ctx, c, id, key); err != nil {
		return err
	}
	if err := s.LUKS.Resize(ctx, id, key); err != nil {
		return err
	}
	if err := s.LUKS.Mount(ctx, id, inst.DataPath); err != nil {
		return err
	}
	if err := s.LUKS.Resize2fs(ctx, id); err != nil {
		return err
	}
	if inst.Stopped {
		return nil
	}
	if err := s.Systemd.Start(ctx, systemd.Unit(id)); err != nil {
		return err
	}
	return s.WaitHealthy(ctx, inst.HTTPPort, 90*time.Second)
}

// ContainerUse reports how full an encrypted instance's file system is.
func (s *Sys) ContainerUse(ctx context.Context, id string) (used, total uint64, err error) {
	inst, err := s.Instance(id)
	if err != nil {
		return 0, 0, err
	}
	if !s.LUKS.IsMounted(ctx, inst.DataPath) {
		return 0, 0, errors.New("locked")
	}
	total, err = fsutil.TotalBytes(inst.DataPath)
	if err != nil {
		return 0, 0, err
	}
	free, err := fsutil.FreeBytes(inst.DataPath)
	return total - free, total, err
}
