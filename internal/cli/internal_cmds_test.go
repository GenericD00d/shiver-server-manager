package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sudoers rule lets admins run the binary as root with any arguments,
// so the hidden internal commands must refuse root, or they would be a way
// past the login.
func TestInternalCommandsRefuseRoot(t *testing.T) {
	old := isRoot
	isRoot = func() bool { return true }
	t.Cleanup(func() { isRoot = old })

	dir := t.TempDir()
	dst := filepath.Join(dir, "copy.sqlite")
	for _, args := range [][]string{
		{"internal-vacuum", filepath.Join(dir, "db.sqlite"), dst},
		{"internal-enable-plugins", filepath.Join(dir, "db.sqlite")},
		{"internal-set-token", filepath.Join(dir, "db.sqlite")},
	} {
		code, _, errOut := execute(args...)
		if code != ExitUsage || !strings.Contains(errOut, "own user") {
			t.Errorf("%v as root: code %d, want %d (%s)", args, code, ExitUsage, errOut)
		}
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Errorf("internal-vacuum wrote %s while refusing root", dst)
	}
}
