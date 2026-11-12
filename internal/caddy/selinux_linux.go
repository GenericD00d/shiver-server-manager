//go:build linux

package caddy

import (
	"strings"

	"golang.org/x/sys/unix"
)

// Confined reports whether SELinux runs Caddy as a confined web server
// (openSUSE labels /usr/bin/caddy httpd_exec_t), which can't create its
// admin socket in /var/lib/caddy.
func Confined() bool {
	buf := make([]byte, 256)
	n, err := unix.Getxattr("/usr/bin/caddy", "security.selinux", buf)
	return err == nil && strings.Contains(string(buf[:n]), ":httpd_exec_t:")
}
