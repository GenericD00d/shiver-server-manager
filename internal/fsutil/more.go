package fsutil

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
)

// SymlinkAtomic points link at target, replacing any existing link in one
// rename.
func SymlinkAtomic(target, link string) error {
	tmp := filepath.Join(filepath.Dir(link), "."+filepath.Base(link)+".tmp-link")
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(link))
}

// SameContent reports whether path holds exactly data.
func SameContent(path string, data []byte) bool {
	b, err := os.ReadFile(path)
	return err == nil && bytes.Equal(b, data)
}

// LinkPointsTo reports whether link is a symlink to target.
func LinkPointsTo(link, target string) bool {
	t, err := os.Readlink(link)
	return err == nil && t == target
}

// RemoveIfExists deletes a file; already gone is fine.
func RemoveIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Exists reports whether path exists, without following a final symlink.
func Exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
