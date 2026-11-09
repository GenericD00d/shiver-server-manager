//go:build linux

package cli

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

// TestE2ELoginAndVault: milestone 2.
func TestE2ELoginAndVault(t *testing.T) {
	e := newE2E(t)
	bin, admin, outsider, pw := e.bin, e.admin, e.outsider, e.pw
	const pass1, pass2 = e2ePass1, e2ePass2

	t.Run("setup shows the recovery key once and needs it confirmed", func(t *testing.T) {
		cmd := exec.Command(bin, "setup", "--password-stdin", "--passphrase-stdin")
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "SUDO_USER=" + admin}
		stdin, _ := cmd.StdinPipe()
		stdout, _ := cmd.StdoutPipe()
		var errb strings.Builder
		cmd.Stderr = &errb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		io.WriteString(stdin, pw+"\n"+pass1+"\n")
		var key string
		var out strings.Builder
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			out.WriteString(sc.Text() + "\n")
			if s := strings.TrimSpace(sc.Text()); strings.HasPrefix(s, "AGE-SECRET-KEY-1") {
				key = s
				break
			}
		}
		if key == "" {
			cmd.Wait()
			t.Fatalf("no recovery key shown\nstdout: %s\nstderr: %s", out.String(), errb.String())
		}
		io.WriteString(stdin, strings.ToLower(key[len(key)-6:])+"\n")
		stdin.Close()
		rest, _ := io.ReadAll(stdout)
		if err := cmd.Wait(); err != nil {
			t.Fatalf("setup: %v\nstdout: %s%s\nstderr: %s", err, out.String(), rest, errb.String())
		}
		for _, d := range setupDirs {
			fi, err := os.Stat(d.path)
			if err != nil || fi.Mode().Perm() != d.mode {
				t.Errorf("%s: %v %v", d.path, fi.Mode().Perm(), err)
			}
		}
		log, _ := os.ReadFile(paths.LogFile)
		if strings.Contains(string(log), key) || strings.Contains(string(log), pass1) || strings.Contains(string(log), pw) {
			t.Fatal("a secret reached the log")
		}
	})

	t.Run("setup again keeps the vault", func(t *testing.T) {
		if out := e.expect(t, 0, admin, pw+"\n", "setup", "--password-stdin"); !strings.Contains(out, "already set up") {
			t.Fatalf("output: %s", out)
		}
	})

	t.Run("vault unlock", func(t *testing.T) {
		e.expect(t, 0, admin, pw+"\n"+pass1+"\n", "vault", "unlock", "--password-stdin", "--passphrase-stdin")
		e.expect(t, ExitAuth, admin, pw+"\nwrong passphrase here\n", "vault", "unlock", "--password-stdin", "--passphrase-stdin")
	})

	t.Run("wrong password refused", func(t *testing.T) {
		e.expect(t, ExitAuth, admin, pw+"x\n", "admins", "list", "--password-stdin")
	})

	t.Run("user outside the group refused", func(t *testing.T) {
		if out := e.expect(t, ExitAuth, outsider, pw+"\n", "admins", "list", "--password-stdin"); !strings.Contains(out, "isn't in the "+paths.AdminGroup) {
			t.Fatalf("output: %s", out)
		}
	})

	t.Run("no terminal and no stdin flag", func(t *testing.T) {
		e.expect(t, ExitAuth, admin, "", "admins", "list")
	})

	t.Run("admins", func(t *testing.T) {
		if out := e.expect(t, 0, admin, pw+"\n", "admins", "list", "--password-stdin", "--json"); !strings.Contains(out, `"`+admin+`"`) {
			t.Fatalf("output: %s", out)
		}
		e.expect(t, 0, admin, pw+"\n", "admins", "add", outsider, "--password-stdin")
		e.expect(t, 0, outsider, pw+"\n", "admins", "list", "--password-stdin")
		e.expect(t, 0, admin, pw+"\n", "admins", "remove", outsider, "--password-stdin")
		e.expect(t, ExitAuth, outsider, pw+"\n", "admins", "list", "--password-stdin")
		e.expect(t, ExitError, admin, pw+"\n", "admins", "remove", admin, "--password-stdin") // last admin
		e.expect(t, ExitError, admin, pw+"\n", "admins", "add", "no-such-account", "--password-stdin")
	})

	t.Run("change passphrase", func(t *testing.T) {
		e.expect(t, 0, admin, pw+"\n"+pass1+"\n"+pass2+"\n", "vault", "change-passphrase", "--password-stdin", "--passphrase-stdin")
		e.expect(t, ExitAuth, admin, pw+"\n"+pass1+"\n", "vault", "unlock", "--password-stdin", "--passphrase-stdin")
		e.expect(t, 0, admin, pw+"\n"+pass2+"\n", "vault", "unlock", "--password-stdin", "--passphrase-stdin")
	})

	t.Run("root without sudo skips the login", func(t *testing.T) {
		e.expect(t, 0, "", "", "admins", "list")
	})

	t.Run("held lock", func(t *testing.T) {
		lk, err := state.Acquire(paths.LockFile, state.Exclusive, state.Holder(1, "someone"), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer lk.Release()
		if out := e.expect(t, ExitLocked, "", "", "admins", "list"); !strings.Contains(out, "user someone") {
			t.Fatalf("output: %s", out)
		}
	})
}
