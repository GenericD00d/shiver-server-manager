//go:build !unix

package fsutil

// Development stand-ins: report plenty of space.
func FreeBytes(string) (uint64, error)  { return 1 << 40, nil }
func TotalBytes(string) (uint64, error) { return 2 << 40, nil }
