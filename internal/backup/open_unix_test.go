//go:build unix

package backup

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Between the walk and the open, the instance's user could swap a file for a
// pipe. Opening it must neither block (the backup holds the manager's lock)
// nor archive it.
func TestAddFileRefusesAPipeWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "swapped"), 0o600); err != nil {
		t.Skip("no mkfifo here:", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	done := make(chan error, 1)
	go func() {
		done <- addFile(tar.NewWriter(&bytes.Buffer{}), root, "swapped", "data/swapped")
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "isn't a plain file") {
			t.Fatalf("a pipe was archived, or failed oddly: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a pipe blocked the backup")
	}
}

// A file swapped for a link to a root-only file must be refused, not read.
func TestAddFileRefusesALinkOutOfTheRoot(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("root only"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "db.sqlite")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var buf bytes.Buffer
	if err := addFile(tar.NewWriter(&buf), root, "db.sqlite", "db.sqlite"); err == nil {
		t.Fatal("followed a link out of the root")
	}
	if bytes.Contains(buf.Bytes(), []byte("root only")) {
		t.Fatal("the root-only file reached the archive")
	}
}
