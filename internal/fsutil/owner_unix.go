//go:build unix

package fsutil

import (
	"os"
	"syscall"
)

// OwnerOf returns a path's UID and GID, without following symlinks.
func OwnerOf(path string) (uid, gid int, err error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	st := fi.Sys().(*syscall.Stat_t)
	return int(st.Uid), int(st.Gid), nil
}
