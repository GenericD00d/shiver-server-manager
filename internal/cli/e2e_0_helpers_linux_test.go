//go:build linux

package cli

// End-to-end tests drive the built binary as root on a throwaway machine
// (CI). They change the real system, so they only run when asked:
//
//	SSM_E2E_BIN: the built binary, root-owned
//	SSM_PAM_ADMIN, SSM_PAM_OUTSIDER: usernames (the first in shiver-admins)
//	SSM_PAM_PASSWORD: both accounts' password
//
// Files run in name order (e2e_1_..., e2e_2_...), each building on the last.

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

// e2eSetupFlags answers setup's questions and points Caddy at Pebble
// (SSM_ACME_CA, SSM_ACME_CA_ROOT) when CI runs one.
func e2eSetupFlags() []string {
	f := []string{"--yes", "--container-size", "1"}
	if ca := os.Getenv("SSM_ACME_CA"); ca != "" {
		f = append(f, "--acme-ca", ca, "--acme-ca-root", os.Getenv("SSM_ACME_CA_ROOT"))
	}
	if ip := os.Getenv("SSM_PUBLIC_IP"); ip != "" {
		f = append(f, "--public-ip", ip) // CI's real public address is NAT'd away
	}
	return f
}

// Vault passphrases: the login test sets pass1, then changes it to pass2.
const e2ePass1, e2ePass2 = "first vault passphrase", "second vault passphrase"

type e2e struct {
	bin, admin, outsider string
	pw                   string
}

func newE2E(t *testing.T) *e2e {
	e := &e2e{bin: os.Getenv("SSM_E2E_BIN"), admin: os.Getenv("SSM_PAM_ADMIN"),
		outsider: os.Getenv("SSM_PAM_OUTSIDER"), pw: os.Getenv("SSM_PAM_PASSWORD")}
	if e.bin == "" || e.admin == "" || os.Geteuid() != 0 {
		t.Skip("set SSM_E2E_BIN and the SSM_PAM_* variables, and run as root on a throwaway machine")
	}
	return e
}

// e2eSharkord is the Sharkord release the end-to-end tests create and import
// servers on. Not the newest: Sharkord 0.0.31 doesn't exit on SIGTERM, so every
// stop waits out systemd's 90 seconds and the suite runs past its timeout
// (docs/decisions.md, 2026-11-14). Move it on once a release stops cleanly.
const e2eSharkord = "0.0.30"

// pinned adds --version e2eSharkord to a create or import that doesn't name one.
func pinned(args []string) []string {
	creates := len(args) > 0 && args[0] == "import" || len(args) > 1 && args[0] == "instance" && args[1] == "create"
	if !creates || slices.Contains(args, "--version") {
		return args
	}
	return append(append([]string{}, args...), "--version", e2eSharkord)
}

func (e *e2e) run(t *testing.T, sudoUser, stdin string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(e.bin, pinned(args)...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
	if sudoUser != "" {
		cmd.Env = append(cmd.Env, "SUDO_USER="+sudoUser)
	}
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var exitErr *exec.ExitError
	code := 0
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, out.String(), errb.String()
}

// expect runs and checks the exit code, returning stdout+stderr.
func (e *e2e) expect(t *testing.T, want int, sudoUser, stdin string, args ...string) string {
	t.Helper()
	code, out, errOut := e.run(t, sudoUser, stdin, args...)
	if code != want {
		t.Fatalf("%v: exit %d, want %d\nstdout: %s\nstderr: %s", args, code, want, out, errOut)
	}
	return out + errOut
}

// admin runs as the admin, logging in with the password on stdin, followed
// by any further stdin lines.
func (e *e2e) asAdmin(t *testing.T, want int, moreStdin string, args ...string) string {
	t.Helper()
	return e.expect(t, want, e.admin, e.pw+"\n"+moreStdin, append(args, "--password-stdin", "--passphrase-stdin")...)
}

// stdout runs as the admin and returns only stdout, which must exit 0.
func (e *e2e) stdout(t *testing.T, moreStdin string, args ...string) string {
	t.Helper()
	code, out, errOut := e.run(t, e.admin, e.pw+"\n"+moreStdin, append(args, "--password-stdin", "--passphrase-stdin")...)
	if code != 0 {
		t.Fatalf("%v: exit %d\nstdout: %s\nstderr: %s", args, code, out, errOut)
	}
	return out
}

func runnerForTest() run.Runner { return &run.Exec{} }

// firewallText is the active firewall's rules as text: ufw's numbered
// status, or firewalld's default zone followed by each sharkord service.
func firewallText(t *testing.T) (name, text string) {
	t.Helper()
	if out, err := exec.Command("firewall-cmd", "--state").Output(); err == nil && strings.TrimSpace(string(out)) == "running" {
		zone, _ := exec.Command("firewall-cmd", "--list-all").CombinedOutput()
		text = string(zone)
		svcs, _ := exec.Command("firewall-cmd", "--list-services").Output()
		for _, s := range strings.Fields(string(svcs)) {
			if strings.HasPrefix(s, "sharkord-") {
				info, _ := exec.Command("firewall-cmd", "--info-service="+s).CombinedOutput()
				text += "\n" + string(info)
			}
		}
		return "firewalld", text
	}
	out, _ := exec.Command("ufw", "status", "numbered").CombinedOutput()
	return "ufw", string(out)
}
