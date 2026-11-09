//go:build windows

package state

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Development-only stand-in for flock. It locks a byte far past the end of
// the file, so the holder text stays readable by waiting runs.
func lockRegion() *windows.Overlapped {
	return &windows.Overlapped{Offset: 0, OffsetHigh: 0x7fffffff}
}

func tryLock(f *os.File, mode LockMode) (bool, error) {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if mode == Exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, lockRegion())
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func unlock(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, lockRegion())
}
