// Package fsutil holds the one way the manager writes files.
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// Owner sets a file's owner. A nil *Owner leaves it as the running user.
type Owner struct{ UID, GID int }

// WriteFileAtomic writes data to a temp file in the target's folder, sets
// mode and owner, fsyncs it, renames it over path, then fsyncs the folder.
// Readers see either the old file or the new one, never a partial one.
func WriteFileAtomic(path string, data []byte, perm os.FileMode, owner *Owner) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err = f.Chmod(perm); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if owner != nil {
		if err = f.Chown(owner.UID, owner.GID); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return syncDir(dir)
}
