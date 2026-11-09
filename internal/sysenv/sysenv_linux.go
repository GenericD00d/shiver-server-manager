//go:build linux

package sysenv

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/GenericD00d/shiver-server-manager/internal/paths"
)

// Setup captures Info, replaces the environment with a fixed one (dropping
// EDITOR, PAGER, SHELL, LD_* and the rest), and sets the umask to 077.
func Setup() Info {
	info := capture()
	os.Clearenv()
	os.Setenv("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
	os.Setenv("LC_ALL", "C")
	os.Setenv("LANG", "C.UTF-8")
	if info.Term != "" {
		os.Setenv("TERM", info.Term)
	}
	if info.NoColor {
		os.Setenv("NO_COLOR", "1")
	}
	unix.Umask(0o077)
	return info
}

func IsRoot() bool { return os.Geteuid() == 0 }

// CheckOwnership refuses to go on if the binary, the config folder or the
// state folder can be changed by anyone but root. Missing folders are fine
// (setup creates them).
func CheckOwnership() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	for _, p := range []string{exe, paths.ConfigDir, paths.StateDir} {
		fi, err := os.Stat(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			continue
		}
		if st.Uid != 0 || fi.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("refusing to run: %s can be changed by users other than root (owner uid %d, mode %v)", p, st.Uid, fi.Mode().Perm())
		}
	}
	return nil
}
