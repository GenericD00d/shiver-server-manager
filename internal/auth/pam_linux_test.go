//go:build linux && cgo

package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/msteinert/pam/v2"
)

func confDir(t *testing.T) string {
	t.Helper()
	if !pam.CheckPamHasStartConfdir() {
		t.Skip("libpam has no pam_start_confdir")
	}
	dir := t.TempDir()
	files := map[string]string{
		"permit": "auth required pam_permit.so\naccount required pam_permit.so\n",
		"deny":   "auth requisite pam_deny.so\naccount required pam_permit.so\n",
		"echo":   "auth optional pam_echo.so Hello %u\nauth required pam_permit.so\naccount required pam_permit.so\n",
		"noacct": "auth required pam_permit.so\naccount requisite pam_deny.so\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPAMServices(t *testing.T) {
	dir := confDir(t)
	if err := (PAM{Service: "permit", ConfDir: dir}).Authenticate("anyone", &answers{}); err != nil {
		t.Fatalf("permit: %v", err)
	}
	if err := (PAM{Service: "deny", ConfDir: dir}).Authenticate("anyone", &answers{}); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("deny: %v", err)
	}
	if err := (PAM{Service: "noacct", ConfDir: dir}).Authenticate("anyone", &answers{}); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("account refused: %v", err)
	}
	a := &answers{}
	if err := (PAM{Service: "echo", ConfDir: dir}).Authenticate("alice", a); err != nil {
		t.Fatalf("echo: %v", err)
	}
	if len(a.infos) != 1 || a.infos[0] != "Hello alice" {
		t.Fatalf("PAM messages not forwarded: %q", a.infos)
	}
}

// TestPAMIntegration runs against the real /etc/pam.d/shiver-server-manager
// and real accounts. CI creates them and runs this as root:
//
//	SSM_PAM_ADMIN, SSM_PAM_OUTSIDER: usernames (the first in shiver-admins)
//	SSM_PAM_PASSWORD: both accounts' password
func TestPAMIntegration(t *testing.T) {
	admin, outsider, pw := os.Getenv("SSM_PAM_ADMIN"), os.Getenv("SSM_PAM_OUTSIDER"), os.Getenv("SSM_PAM_PASSWORD")
	if admin == "" {
		t.Skip("set SSM_PAM_ADMIN, SSM_PAM_OUTSIDER and SSM_PAM_PASSWORD, and run as root")
	}
	l := &Login{Auth: PAM{Service: "shiver-server-manager"}}
	if err := l.Run(admin, &answers{hidden: []string{pw}}); err != nil {
		t.Fatalf("admin with the right password: %v", err)
	}
	if err := l.Run(admin, &answers{hidden: []string{pw + "x"}}); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := l.Run(outsider, &answers{hidden: []string{pw}}); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("user outside the group: %v", err)
	}
	if err := l.Run("no-such-user-here", &answers{hidden: []string{pw}}); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("unknown user: %v", err)
	}
}
