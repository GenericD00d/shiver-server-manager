//go:build unix

package state

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLock(f *os.File, mode LockMode) (bool, error) {
	how := unix.LOCK_SH
	if mode == Exclusive {
		how = unix.LOCK_EX
	}
	err := unix.Flock(int(f.Fd()), how|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return false, nil
	}
	return err == nil, err
}

func unlock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_UN) }
