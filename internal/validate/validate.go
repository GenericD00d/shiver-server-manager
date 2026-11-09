// Package validate checks input before it goes into any name or path.
package validate

import (
	"fmt"
	"regexp"
)

var instanceID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)

// InstanceID checks a server ID: lowercase letters, digits and hyphens,
// starting with a letter, at most 20 characters. The ID goes into folder,
// service, user, Caddy file and firewall names, and these rules keep it
// valid in all of them.
func InstanceID(id string) error {
	if !instanceID.MatchString(id) {
		return fmt.Errorf("invalid server ID %q: use lowercase letters, digits and hyphens, start with a letter, and keep it to 20 characters", id)
	}
	return nil
}

var unixName = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}\$?$`)

// Username checks an OS account name before it reaches a command line.
// It is stricter than Linux needs, but matches Debian's default rule.
func Username(name string) error {
	if !unixName.MatchString(name) {
		return fmt.Errorf("invalid username %q", name)
	}
	return nil
}
