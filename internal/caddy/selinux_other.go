//go:build !linux

package caddy

// Confined is always false off Linux.
func Confined() bool { return false }
