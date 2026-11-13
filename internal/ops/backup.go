package ops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/GenericD00d/shiver-server-manager/internal/backup"
	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// stagingDir makes a private folder inside the instance's data folder (so
// it's inside the container for an encrypted instance), owned by root, mode
// 700. Root unpacks into it and swaps data through it, so the instance's own
// user must not be able to change what's inside while that happens; the
// files are given to that user as they're written (backup.Read) or once
// they're in place (swapPlugin).
func (s *Sys) stagingDir(id, prefix string) (string, error) {
	inst := s.St.Instances[id]
	dir := filepath.Join(inst.DataPath, ".ssm-"+prefix+"-"+s.Now().UTC().Format("20060102T150405.000000000"))
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// vacuumDir is a staging folder the instance's own user can write, for the
// database copy that internal-vacuum makes as that user. Root only reads from
// it, through an os.Root (backup.Write).
func (s *Sys) vacuumDir(id string) (string, error) {
	dir, err := s.stagingDir(id, "backup")
	if err != nil {
		return "", err
	}
	uid, gid, err := s.UserIDs(osuser.InstanceUser(id))
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if err := s.Chown(dir, uid, gid); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func (s *Sys) requireUnlocked(ctx context.Context, id string) (*state.Instance, error) {
	inst, err := s.requireActive(id)
	if err != nil {
		return nil, err
	}
	if inst.Encrypted && !s.LUKS.IsMounted(ctx, inst.DataPath) {
		return nil, fmt.Errorf("%s is locked; run 'vault unlock' first", id)
	}
	return inst, nil
}

// writeArchive copies the database with VACUUM INTO (as the instance's
// user), then streams the archive to w. The instance keeps running.
func (s *Sys) writeArchive(ctx context.Context, id, kind string, w io.Writer, r age.Recipient, token *string) error {
	inst := s.St.Instances[id]
	staging, err := s.vacuumDir(id)
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	uid, gid, err := s.UserIDs(osuser.InstanceUser(id))
	if err != nil {
		return err
	}
	dbCopy := filepath.Join(staging, "db.sqlite")
	if err := s.AsInstance(ctx, uid, gid, "", "internal-vacuum", sharkord.DBPath(inst.DataPath), dbCopy); err != nil {
		return err
	}
	m := backup.Manifest{Format: backup.FormatVersion, Kind: kind, CreatedAt: s.Now().UTC(),
		ManagerVersion: s.ManagerVersion, SharkordVersion: inst.SharkordVersion, OwnerToken: token}
	m.Instance.ID, m.Instance.Domain, m.Instance.TLS = id, inst.Domain, string(inst.TLS)
	m.Instance.Encrypted, m.Instance.Settings = inst.Encrypted, inst.Settings
	return backup.Write(w, backup.WriteOpts{Manifest: m, DataPath: inst.DataPath, DBCopy: dbCopy, Recipient: r,
		Note: func(n string) { s.emit(Event{Instance: id, Step: "backup", Status: "note", Detail: n}) }})
}

// writeFileAtomic writes an archive to a temp file beside path, then
// renames it into place with mode 600.
func writeAtomically(path string, fill func(io.Writer) error) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if err := fill(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// BackupDir is where an instance's backups go.
func (s *Sys) BackupDir(id string) string {
	return filepath.Join(s.St.Settings.BackupLocation, id)
}

// Backup makes an encrypted backup to the vault's public key (no unlock
// needed) and prunes old ones. It returns the file.
func (s *Sys) Backup(ctx context.Context, id string) (string, error) {
	if _, err := s.requireUnlocked(ctx, id); err != nil {
		return "", err
	}
	r, err := s.Vault.Recipient()
	if err != nil {
		return "", err
	}
	dir := s.BackupDir(id)
	if err := fsutil.MkdirAllMode(dir, 0o700); err != nil {
		return "", err
	}
	now := s.Now()
	file := filepath.Join(dir, backup.FileName(now))
	if err := writeAtomically(file, func(w io.Writer) error { return s.writeArchive(ctx, id, "backup", w, r, nil) }); err != nil {
		return "", err
	}
	fi, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	sum, err := backup.Sum(file)
	if err != nil {
		return "", err
	}
	if _, err := backup.Record(dir, backup.Entry{File: filepath.Base(file), CreatedAt: now.UTC(), Size: fi.Size(), SHA256: sum}, 0); err != nil {
		return file, err
	}
	removed, err := backup.Prune(dir, s.retention(s.St.Instances[id].Backups), now)
	for _, r := range removed {
		s.emit(Event{Instance: id, Step: "backup", Status: "note", Detail: "removed old backup " + r})
	}
	return file, err
}

// Export writes an archive encrypted with its own passphrase, so it can move
// to another server without sharing the vault. The owner token goes inside
// only when asked (which needs the vault unlocked).
func (s *Sys) Export(ctx context.Context, id, out, passphrase string, includeToken bool) error {
	if _, err := s.requireUnlocked(ctx, id); err != nil {
		return err
	}
	if err := vault.CheckPassphrase(passphrase); err != nil {
		return err
	}
	r, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return err
	}
	var token *string
	if includeToken {
		t, err := s.Token(s.VaultKey, id)
		if err != nil {
			return err
		}
		token = &t
	}
	out, err = filepath.Abs(out)
	if err != nil {
		return err
	}
	if fsutil.Exists(out) {
		return fmt.Errorf("%s already exists", out)
	}
	return writeAtomically(out, func(w io.Writer) error { return s.writeArchive(ctx, id, "export", w, r, token) })
}

// Backups lists an instance's backups, oldest first.
func (s *Sys) Backups(id string) ([]backup.Entry, error) {
	ix, err := backup.LoadIndex(s.BackupDir(id))
	return ix.Backups, err
}

// unpackInto decrypts an archive into a new staging folder inside the data
// folder, with files owned by the instance (folders 700, files 600).
func (s *Sys) unpackInto(id, file string, identity age.Identity, prefix string) (string, backup.Manifest, error) {
	staging, err := s.stagingDir(id, prefix)
	if err != nil {
		return "", backup.Manifest{}, err
	}
	uid, gid, err := s.UserIDs(osuser.InstanceUser(id))
	if err != nil {
		return "", backup.Manifest{}, err
	}
	f, err := os.Open(file)
	if err != nil {
		os.RemoveAll(staging)
		return "", backup.Manifest{}, err
	}
	defer f.Close()
	m, err := backup.Read(f, backup.ReadOpts{Identity: identity, Dest: staging,
		Chown: func(p string) error { return s.Chown(p, uid, gid) }})
	if err != nil {
		os.RemoveAll(staging)
		return "", m, err
	}
	return staging, m, nil
}

// IdentityFor works out how to open an archive: the vault key for a backup,
// or ask (the passphrase callback) for an export.
func (s *Sys) IdentityFor(file string, passphrase func() (string, error)) (age.Identity, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	kind, err := backup.Detect(f)
	if err != nil {
		return nil, err
	}
	switch kind {
	case backup.VaultKey:
		if s.VaultKey == nil {
			return nil, ErrVaultLocked
		}
		return s.VaultKey.Identity(), nil
	default:
		p, err := passphrase()
		if err != nil {
			return nil, err
		}
		id, err := age.NewScryptIdentity(p)
		if err != nil {
			return nil, err
		}
		return id, nil
	}
}

// topEntries lists a folder's entries except the manager's own staging
// folders and lost+found.
func topEntries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".ssm-") || e.Name() == "lost+found" {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}

func moveEntries(from, to string) error {
	names, err := topEntries(from)
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := os.Rename(filepath.Join(from, n), filepath.Join(to, n)); err != nil {
			return err
		}
	}
	return nil
}

// Restore replaces an instance's data with an archive's. The archive is
// unpacked beside the current data, the folders are swapped while the
// instance is stopped, and swapped back if it doesn't come up. It returns
// the folder holding the previous data, for the admin to delete.
func (s *Sys) Restore(ctx context.Context, id, file string, identity age.Identity) (string, error) {
	inst, err := s.requireUnlocked(ctx, id)
	if err != nil {
		return "", err
	}
	staging, m, err := s.unpackInto(id, file, identity, "restore")
	if err != nil {
		return "", err
	}
	if sharkord.CompareVersions(m.SharkordVersion, inst.SharkordVersion) > 0 {
		os.RemoveAll(staging)
		return "", fmt.Errorf("the archive was made by Sharkord %s, newer than this server's %s; update the server first", m.SharkordVersion, inst.SharkordVersion)
	}
	old, err := s.stagingDir(id, "old")
	if err != nil {
		os.RemoveAll(staging)
		return "", err
	}
	unit := systemd.Unit(id)
	if err := s.Systemd.Stop(ctx, unit); err != nil {
		return "", err
	}
	swapIn := func() error {
		if err := moveEntries(inst.DataPath, old); err != nil {
			return err
		}
		return moveEntries(staging, inst.DataPath)
	}
	if err := swapIn(); err != nil {
		return "", fmt.Errorf("swapping in the restored data failed; the old data is in %s: %w", old, err)
	}
	os.RemoveAll(staging)
	if err := s.Systemd.Start(ctx, unit); err == nil {
		if err = s.WaitHealthy(ctx, inst.HTTPPort, 90*time.Second); err == nil {
			return old, nil
		}
	}
	// Didn't come up: put the old data back.
	_ = s.Systemd.Stop(ctx, unit)
	failed, ferr := s.stagingDir(id, "failed-restore")
	if ferr == nil {
		ferr = moveEntries(inst.DataPath, failed)
	}
	if ferr == nil {
		ferr = moveEntries(old, inst.DataPath)
	}
	if ferr != nil {
		return "", fmt.Errorf("the restored server didn't start, and putting the old data back failed (it's in %s): %w", old, ferr)
	}
	os.RemoveAll(old)
	_ = s.Systemd.Start(ctx, unit)
	return "", fmt.Errorf("the restored server didn't start, so the previous data is back (the restored copy is in %s)", failed)
}

// DeleteLeftover removes a folder Restore left: <data>/.ssm-old-* or
// .ssm-failed-restore-*.
func (s *Sys) DeleteLeftover(id, dir string) error {
	inst, err := s.Instance(id)
	if err != nil {
		return err
	}
	base := filepath.Base(dir)
	if filepath.Dir(filepath.Clean(dir)) != inst.DataPath || !(strings.HasPrefix(base, ".ssm-old-") || strings.HasPrefix(base, ".ssm-failed-restore-")) {
		return fmt.Errorf("refusing to delete %s", dir)
	}
	return os.RemoveAll(dir)
}

// Update moves an instance to another tested Sharkord version: back up,
// download, switch the version link, restart. If /healthz doesn't answer
// within 90 seconds, the link goes back and the database from the backup is
// restored, since the old version can't open a database the new one has
// already upgraded.
func (s *Sys) Update(ctx context.Context, id, version string) error {
	inst, err := s.requireUnlocked(ctx, id)
	if err != nil {
		return err
	}
	if version == "" {
		version = sharkord.LatestTested().Version
	}
	if _, ok := sharkord.FindTested(version); !ok {
		return fmt.Errorf("Sharkord %s isn't on the tested list; tested: %s", version, testedList())
	}
	if version == inst.SharkordVersion {
		return fmt.Errorf("%s already runs Sharkord %s", id, version)
	}
	if sharkord.CompareVersions(version, inst.SharkordVersion) < 0 {
		return fmt.Errorf("going back to an older Sharkord isn't possible once the database has been upgraded; restore a backup instead")
	}
	if s.VaultKey == nil {
		return ErrVaultLocked // to restore the database if the update fails
	}
	s.emit(Event{Instance: id, Step: "backup", Status: "run"})
	file, err := s.Backup(ctx, id)
	if err != nil {
		return err
	}
	s.emit(Event{Instance: id, Step: "backup", Status: "done", Detail: file})
	if err := s.Sharkord.Install(ctx, s.Paths.OptDir, version); err != nil {
		return err
	}
	unit := systemd.Unit(id)
	oldVersion := inst.SharkordVersion
	if err := s.Systemd.Stop(ctx, unit); err != nil {
		return err
	}
	inst.SharkordVersion = version
	if err := s.writeFiles(ctx, id); err != nil {
		return err
	}
	if err := s.Save(); err != nil {
		return err
	}
	if err := s.Systemd.Start(ctx, unit); err == nil {
		if err = s.WaitHealthy(ctx, inst.HTTPPort, 90*time.Second); err == nil {
			s.emit(Event{Instance: id, Step: "update", Status: "done", Detail: "now on Sharkord " + version})
			s.PruneVersions()
			return nil
		}
	}
	// Roll back: old link, database from the backup.
	_ = s.Systemd.Stop(ctx, unit)
	inst.SharkordVersion = oldVersion
	if err := s.writeFiles(ctx, id); err != nil {
		return err
	}
	if err := s.Save(); err != nil {
		return err
	}
	staging, _, err := s.unpackInto(id, file, s.VaultKey.Identity(), "rollback")
	if err != nil {
		return fmt.Errorf("Sharkord %s didn't start, and the backup couldn't be read to roll back (%s): %w", version, file, err)
	}
	defer os.RemoveAll(staging)
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = fsutil.RemoveIfExists(sharkord.DBPath(inst.DataPath) + suffix)
	}
	if err := os.Rename(filepath.Join(staging, "db.sqlite"), sharkord.DBPath(inst.DataPath)); err != nil {
		return err
	}
	if err := s.Systemd.Start(ctx, unit); err != nil {
		return err
	}
	if err := s.WaitHealthy(ctx, inst.HTTPPort, 90*time.Second); err != nil {
		return fmt.Errorf("Sharkord %s didn't start; rolled back to %s, but that didn't answer either: %w", version, oldVersion, err)
	}
	return fmt.Errorf("Sharkord %s didn't start, so %s is back on %s with its database from the backup taken just before", version, id, oldVersion)
}

// PruneVersions removes Sharkord versions no instance links to, except the
// newest.
func (s *Sys) PruneVersions() {
	entries, _ := os.ReadDir(s.Paths.OptDir)
	newest := ""
	for _, e := range entries {
		if sharkord.ValidVersion(e.Name()) == nil && sharkord.CompareVersions(e.Name(), newest) > 0 {
			newest = e.Name()
		}
	}
	used := map[string]bool{newest: true}
	for _, inst := range s.St.Instances {
		used[inst.SharkordVersion] = true
	}
	for _, e := range entries {
		if sharkord.ValidVersion(e.Name()) == nil && !used[e.Name()] {
			_ = os.RemoveAll(filepath.Join(s.Paths.OptDir, e.Name()))
		}
	}
}

var errNoSource = errors.New("this import was interrupted; run the same import command again to finish it")
