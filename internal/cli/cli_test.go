package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
)

func execute(args ...string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	app := &App{Version: "1.2.3", Out: &out, Err: &errb}
	code = app.Execute(args)
	return code, out.String(), errb.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := execute("version")
	if code != ExitOK || !strings.HasPrefix(out, "shiver-server-manager 1.2.3 (") {
		t.Fatalf("code %d, out %q", code, out)
	}
}

func TestVersionJSON(t *testing.T) {
	code, out, _ := execute("version", "--json")
	var got map[string]string
	if code != ExitOK || json.Unmarshal([]byte(out), &got) != nil || got["version"] != "1.2.3" || got["arch"] != runtime.GOARCH {
		t.Fatalf("code %d, out %q", code, out)
	}
}

func TestUsageErrorsExit2(t *testing.T) {
	for _, args := range [][]string{
		{"no-such-command"},
		{"version", "--no-such-flag"},
		{"version", "extra"},
		{"instance", "show"},
		{"restore", "only-one"},
	} {
		if code, _, errOut := execute(args...); code != ExitUsage {
			t.Errorf("%v: code %d, want %d (%s)", args, code, ExitUsage, errOut)
		}
	}
}

func TestEveryGuideCommandExists(t *testing.T) {
	app := &App{}
	root := app.newRoot()
	for _, path := range []string{
		"setup", "instance list", "instance show", "instance create", "instance start", "instance stop",
		"instance restart", "instance logs", "instance set", "instance update", "instance move", "instance delete",
		"token show", "token save", "shiver status", "shiver install", "shiver update",
		"cert status", "cert switch", "cert retry", "cert root", "backup", "restore", "export", "import",
		"storage list", "storage add", "storage mount", "storage remove", "storage default",
		"firewall status", "firewall repair", "vault unlock", "vault change-passphrase", "vault recover",
		"admins list", "admins add", "admins remove", "health", "repair", "version",
	} {
		c, rest, err := root.Find(strings.Fields(path))
		if err != nil || len(rest) != 0 || c.CommandPath() != "shiver-server-manager "+path {
			t.Errorf("%q: missing (found %q, rest %v, err %v)", path, c.CommandPath(), rest, err)
		}
	}
}

func TestNonLinuxOrNonRootRefused(t *testing.T) {
	if runtime.GOOS == "linux" && os.Geteuid() == 0 {
		t.Skip("running as root, so the command isn't refused")
	}
	code, _, errOut := execute("instance", "list")
	if code != ExitError {
		t.Fatalf("code %d (%s)", code, errOut)
	}
	if runtime.GOOS != "linux" && !strings.Contains(errOut, "only runs on Linux") {
		t.Fatalf("stderr %q", errOut)
	}
}
