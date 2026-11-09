package state

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type LockMode int

const (
	Shared    LockMode = iota // read-only commands
	Exclusive                 // commands that change anything
)

// LockTimeout is how long Acquire waits before giving up.
const LockTimeout = 10 * time.Second

// HeldError means another run holds the lock.
type HeldError struct{ Holder string }

func (e *HeldError) Error() string {
	if e.Holder == "" {
		return "another run of the manager holds the lock"
	}
	return "another run of the manager holds the lock (" + e.Holder + ")"
}

type Lock struct {
	f    *os.File
	mode LockMode
}

// Acquire takes the lock file at path, waiting up to timeout. An exclusive
// holder writes holder (its PID and user) into the file so a waiting run can
// say who has it.
func Acquire(path string, mode LockMode, holder string, timeout time.Duration) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		ok, err := tryLock(f, mode)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("lock: %w", err)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			who := readHolder(f)
			f.Close()
			return nil, &HeldError{Holder: who}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if mode == Exclusive {
		if err := f.Truncate(0); err == nil {
			_, _ = f.WriteAt([]byte(holder+"\n"), 0)
			_ = f.Sync()
		}
	}
	return &Lock{f: f, mode: mode}, nil
}

// Release clears the holder and drops the lock. Safe to call twice.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	if l.mode == Exclusive {
		_ = l.f.Truncate(0)
	}
	err := unlock(l.f)
	if cerr := l.f.Close(); err == nil {
		err = cerr
	}
	l.f = nil
	return err
}

func readHolder(f *os.File) string {
	b, err := io.ReadAll(io.NewSectionReader(f, 0, 256))
	if err != nil && !errors.Is(err, io.EOF) {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Holder formats the text an exclusive holder writes.
func Holder(pid int, user string) string {
	return fmt.Sprintf("pid %d, user %s", pid, user)
}
