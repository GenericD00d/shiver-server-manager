//go:build !unix

package fsutil

// Folders can't be fsynced on Windows; the manager only runs on Linux, and
// this exists so development builds and tests work.
func syncDir(string) error { return nil }
