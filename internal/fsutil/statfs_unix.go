//go:build unix

package fsutil

import "golang.org/x/sys/unix"

// FreeBytes is the space available to unprivileged users at path.
func FreeBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// TotalBytes is the file system's size.
func TotalBytes(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Blocks * uint64(st.Bsize), nil
}
