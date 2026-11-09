//go:build !unix

package fsutil

import "os"

// OwnerOf has no meaning on Windows; it reports 0, 0 for existing paths.
func OwnerOf(path string) (uid, gid int, err error) {
	_, err = os.Lstat(path)
	return 0, 0, err
}
