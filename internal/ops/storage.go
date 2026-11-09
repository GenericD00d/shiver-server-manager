package ops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/storage"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
)

// NeededSpace is the free space a new or moved instance needs.
func (s *Sys) NeededSpace(encrypted bool) int64 {
	if encrypted {
		return int64(s.St.Settings.ContainerSizeGiB)*gib + gib
	}
	return 2 * gib
}

// checkRoom refuses a location without enough space, or one the instance's
// user couldn't pass through to reach its folder.
func (s *Sys) checkRoom(loc string, encrypted bool, extra int64) error {
	need := s.NeededSpace(encrypted) + extra
	if free, err := fsutil.FreeBytes(loc); err != nil {
		return err
	} else if int64(free) < need {
		return fmt.Errorf("only %.1f GiB free at %s; this needs %.1f GiB", float64(free)/float64(gib), loc, float64(need)/float64(gib))
	}
	if blocked := storage.BlockedParents(filepath.Join(loc, "x")); len(blocked) > 0 {
		return fmt.Errorf("server users can't pass through %s (no x permission for others); fix it with: chmod o+x %s", strings.Join(blocked, ", "), strings.Join(blocked, " "))
	}
	return nil
}

// LocationInfo is a location with its drive and use.
type LocationInfo struct {
	state.Location
	Mount     storage.Mount `json:"mount"`
	FreeBytes uint64        `json:"freeBytes"`
	Servers   []string      `json:"servers"`
	Problem   string        `json:"problem,omitempty"`
}

func (s *Sys) Locations(ctx context.Context) []LocationInfo {
	var out []LocationInfo
	for _, l := range s.St.Settings.Locations {
		li := LocationInfo{Location: l}
		m, err := storage.FindMount(ctx, s.R, l.Path)
		if err != nil {
			li.Problem = err.Error()
		} else {
			li.Mount = m
			if l.FSUUID != "" && m.UUID != l.FSUUID {
				li.Problem = fmt.Sprintf("drive missing: expected the drive %s, found %s at %s", l.FSUUID, m.UUID, m.Target)
			}
		}
		li.FreeBytes, _ = fsutil.FreeBytes(l.Path)
		for _, id := range s.IDs() {
			if s.St.Instances[id].Location == l.Path {
				li.Servers = append(li.Servers, id)
			}
		}
		out = append(out, li)
	}
	return out
}

// AddLocation checks a folder's drive and adds it as a storage location.
// It returns parent folders others can't pass through, for the caller to
// offer "chmod o+x"; the location is added either way.
func (s *Sys) AddLocation(ctx context.Context, p string) ([]string, error) {
	p = filepath.Clean(p)
	for _, l := range s.St.Settings.Locations {
		if l.Path == p {
			return nil, fmt.Errorf("%s is already a storage location", p)
		}
	}
	m, err := storage.CheckLocation(ctx, s.R, p, s.Fstab, true)
	if err != nil {
		return nil, err
	}
	if err := fsutil.MkdirAllMode(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	// Root-owned, 711: instance users pass through but can't list it.
	if err := os.Mkdir(p, 0o711); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := os.Chown(p, 0, 0); err != nil {
		return nil, err
	}
	if err := os.Chmod(p, 0o711); err != nil {
		return nil, err
	}
	loc := state.Location{Path: p}
	if m.Target != "/" {
		loc.FSUUID = m.UUID // the health check notices a different drive at the path
	}
	s.St.Settings.Locations = append(s.St.Settings.Locations, loc)
	return storage.BlockedParents(filepath.Join(p, "x")), s.Save()
}

// RemoveLocation removes an unused, non-default location (its folder stays).
func (s *Sys) RemoveLocation(p string) error {
	p = filepath.Clean(p)
	for _, id := range s.IDs() {
		if s.St.Instances[id].Location == p {
			return fmt.Errorf("server %s uses %s; move it first", id, p)
		}
	}
	locs := s.St.Settings.Locations
	for i, l := range locs {
		if l.Path == p {
			if l.Default {
				return errors.New("that's the default location; make another one the default first")
			}
			s.St.Settings.Locations = slices.Delete(locs, i, i+1)
			return s.Save()
		}
	}
	return fmt.Errorf("%s isn't a storage location", p)
}

func (s *Sys) SetDefaultLocation(p string) error {
	p = filepath.Clean(p)
	found := false
	for i := range s.St.Settings.Locations {
		l := &s.St.Settings.Locations[i]
		l.Default = l.Path == p
		found = found || l.Default
	}
	if !found {
		return fmt.Errorf("%s isn't a storage location", p)
	}
	return s.Save()
}

// SetBackupLocation checks and saves where backups and exports go. Any
// local file system will do; backups are encrypted files.
func (s *Sys) SetBackupLocation(ctx context.Context, p string) error {
	p = filepath.Clean(p)
	if _, err := storage.CheckLocation(ctx, s.R, p, s.Fstab, false); err != nil {
		return err
	}
	if err := fsutil.MkdirAllMode(p, 0o700); err != nil {
		return err
	}
	s.St.Settings.BackupLocation = p
	return s.Save()
}

// Move copies an instance's data to another location and switches it over,
// as resumable steps. The old copy is kept; DeleteOldCopy removes it once
// the admin confirms.
func (s *Sys) Move(ctx context.Context, id, to string) error {
	inst, err := s.requireActive(id)
	if err != nil {
		return err
	}
	to = filepath.Clean(to)
	if to == inst.Location {
		return fmt.Errorf("%s is already at %s", id, to)
	}
	known := false
	for _, l := range s.St.Settings.Locations {
		known = known || l.Path == to
	}
	if !known {
		return fmt.Errorf("%s isn't a storage location; add it first", to)
	}
	if fsutil.Exists(filepath.Join(to, id)) || fsutil.Exists(filepath.Join(to, id+".luks")) {
		return fmt.Errorf("%s already has a %s; remove it first", to, id)
	}
	size, err := s.dataSize(ctx, id)
	if err != nil {
		return err
	}
	if err := s.checkRoom(to, false, size); err != nil {
		return err
	}
	inst.Operation = &state.Operation{Name: OpMove, StartedAt: s.Now().UTC(),
		Params: map[string]string{"from": inst.Location, "to": to}}
	if err := s.Save(); err != nil {
		return err
	}
	return s.runSteps(ctx, id, OpMove, s.moveSteps(id))
}

func (s *Sys) dataSize(ctx context.Context, id string) (int64, error) {
	inst := s.St.Instances[id]
	if inst.Encrypted {
		fi, err := os.Stat(ContainerPath(inst, id))
		if err != nil {
			return 0, err
		}
		return fi.Size(), nil
	}
	_, bytes, err := treeStats(inst.DataPath)
	return bytes, err
}

// treeStats counts regular files and bytes, without following links.
func treeStats(root string) (files int, bytes int64, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			files++
			bytes += fi.Size()
		}
		return nil
	})
	return
}

func (s *Sys) moveSteps(id string) []Step {
	inst := s.St.Instances[id]
	op := inst.Operation
	from, to := op.Params["from"], op.Params["to"]
	oldData, newData := filepath.Join(from, id), filepath.Join(to, id)
	oldC, newC := filepath.Join(from, id+".luks"), filepath.Join(to, id+".luks")
	flag := func(k string) func(context.Context) (bool, error) {
		return func(context.Context) (bool, error) { return op.Params[k] == "yes", nil }
	}
	unit := systemd.Unit(id)
	return []Step{
		{
			Name: "stop",
			Done: func(ctx context.Context) (bool, error) {
				if op.Params["switched"] == "yes" {
					return true, nil
				}
				st, err := s.Systemd.Status(ctx, unit)
				if err != nil || st.Active == "active" || st.Active == "activating" {
					return false, err
				}
				return !inst.Encrypted || !s.LUKS.IsMounted(ctx, oldData), nil
			},
			Do: func(ctx context.Context) error {
				if inst.Encrypted {
					return s.Lock(ctx, id)
				}
				return s.Systemd.Stop(ctx, unit)
			},
		},
		{
			Name: "copy",
			Done: flag("copied"),
			Do: func(ctx context.Context) error {
				if inst.Encrypted {
					if err := fsutil.RemoveIfExists(newC); err != nil {
						return err
					}
					if s.isBtrfs(ctx, to) {
						f, err := os.OpenFile(newC, os.O_CREATE|os.O_WRONLY, 0o600)
						if err != nil {
							return err
						}
						f.Close()
						if _, err := s.R.Run(ctx, runCmd("chattr", "+C", newC)); err != nil {
							return err
						}
					}
					if _, err := s.R.Run(ctx, run.Cmd{Name: "rsync", Args: []string{"-a", "--sparse", "--inplace", oldC, newC}, Timeout: 6 * time.Hour}); err != nil {
						return err
					}
				} else {
					if err := os.Mkdir(newData, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
						return err
					}
					if _, err := s.R.Run(ctx, run.Cmd{Name: "rsync", Args: []string{"-aHAX", "--numeric-ids", "--delete", oldData + "/", newData + "/"}, Timeout: 6 * time.Hour}); err != nil {
						return err
					}
				}
				op.Params["copied"] = "yes"
				return nil
			},
		},
		{
			Name: "verify",
			Done: flag("verified"),
			Do: func(ctx context.Context) error {
				if inst.Encrypted {
					// Open the copy where it now is, and check inside.
					key, err := s.luksKey(id)
					if err != nil {
						return err
					}
					defer clear(key)
					if err := s.LUKS.Open(ctx, newC, id, key); err != nil {
						return err
					}
					if err := s.LUKS.Fsck(ctx, id); err != nil {
						return err
					}
					if err := s.LUKS.Mount(ctx, id, newData); err != nil {
						return err
					}
					if err := sharkord.IntegrityCheck(newData); err != nil {
						return err
					}
				} else {
					if err := sharkord.IntegrityCheck(newData); err != nil {
						return err
					}
					of, ob, err := treeStats(oldData)
					if err != nil {
						return err
					}
					nf, nb, err := treeStats(newData)
					if err != nil {
						return err
					}
					if of != nf || ob != nb {
						return fmt.Errorf("the copy differs: %d files, %d bytes there; %d files, %d bytes here", of, ob, nf, nb)
					}
				}
				op.Params["verified"] = "yes"
				return nil
			},
		},
		{
			Name: "switch",
			Done: flag("switched"),
			Do: func(ctx context.Context) error {
				inst.Location, inst.DataPath = to, newData
				if err := s.writeFiles(ctx, id); err != nil {
					return err
				}
				if _, err := s.R.Run(ctx, runCmd("usermod", "-d", newData, osuser.InstanceUser(id))); err != nil {
					return err
				}
				op.Params["switched"] = "yes"
				return nil
			},
		},
		{
			Name: "start",
			Done: func(ctx context.Context) (bool, error) {
				if inst.Stopped {
					return true, nil
				}
				st, err := s.Systemd.Status(ctx, unit)
				if err != nil || st.Active != "active" {
					return false, err
				}
				return s.WaitHealthy(ctx, inst.HTTPPort, 3*time.Second) == nil, nil
			},
			Do: func(ctx context.Context) error {
				if inst.Encrypted {
					if err := s.openAndMount(ctx, id, false); err != nil {
						return err
					}
				}
				if err := s.Systemd.Start(ctx, unit); err != nil {
					return err
				}
				return s.WaitHealthy(ctx, inst.HTTPPort, 90*time.Second)
			},
		},
	}
}

// OldCopy is what a finished move left behind: the old data folder (and
// container) path and its size.
func (s *Sys) OldCopy(id, from string) (paths []string, size int64) {
	for _, p := range []string{filepath.Join(from, id), filepath.Join(from, id+".luks")} {
		if fi, err := os.Lstat(p); err == nil {
			paths = append(paths, p)
			if fi.IsDir() {
				_, b, _ := treeStats(p)
				size += b
			} else {
				size += fi.Size()
			}
		}
	}
	return
}

// DeleteOldCopy removes the old copy after a move. It refuses anything but
// <from>/<id>[.luks] where from isn't the instance's current location.
func (s *Sys) DeleteOldCopy(ctx context.Context, id, from string) error {
	inst, err := s.Instance(id)
	if err != nil {
		return err
	}
	if filepath.Clean(from) == inst.Location {
		return errors.New("that's where the server is now")
	}
	c := filepath.Join(from, id+".luks")
	if inst.Encrypted && fsutil.Exists(c) {
		// Wipe the key slots first, so the old copy is unreadable even
		// before its blocks are reused.
		if err := s.LUKS.Erase(ctx, c); err != nil {
			return err
		}
	}
	for _, p := range []string{filepath.Join(from, id), c} {
		if err := os.RemoveAll(p); err != nil {
			return err
		}
	}
	return nil
}
