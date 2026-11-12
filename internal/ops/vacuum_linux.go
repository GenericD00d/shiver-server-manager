//go:build linux

package ops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// runSelfAs runs one of this binary's hidden internal commands as an
// instance's user, so SQLite never leaves root-owned files beside a
// server's database. stdin, if any, is its input: secrets go there, not
// on the command line, where every user can read them.
func runSelfAs(ctx context.Context, uid, gid int, stdin string, args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, self, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	cmd.Dir = "/"
	cmd.Stdin = strings.NewReader(stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DBInUse finds a process holding the database open, by comparing device
// and inode with every /proc/*/fd target. This also finds processes inside
// containers, whose paths differ.
func DBInUse(db string) (string, error) {
	want, err := os.Stat(db)
	if err != nil {
		return "", err
	}
	wst := want.Sys().(*syscall.Stat_t)
	procs, _ := os.ReadDir("/proc")
	for _, p := range procs {
		if p.Name()[0] < '0' || p.Name()[0] > '9' {
			continue
		}
		fds, _ := os.ReadDir("/proc/" + p.Name() + "/fd")
		for _, fd := range fds {
			fi, err := os.Stat("/proc/" + p.Name() + "/fd/" + fd.Name())
			if err != nil {
				continue
			}
			if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Dev == wst.Dev && st.Ino == wst.Ino {
				comm, _ := os.ReadFile("/proc/" + p.Name() + "/comm")
				return fmt.Sprintf("%s (process %s)", strings.TrimSpace(string(comm)), p.Name()), nil
			}
		}
	}
	return "", nil
}
