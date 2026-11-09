// Package auth is the manager's login: an OS username and password checked
// through PAM, membership of the admins group, a pause after repeated
// failures, and the idle lock.
package auth

import (
	"errors"
	"fmt"
	"os/user"
	"slices"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/validate"
)

// Prompter answers PAM's questions. Every prompt is forwarded rather than
// assuming a single password, so two-factor modules work.
type Prompter interface {
	Hidden(msg string) (string, error) // a password or a code
	Visible(msg string) (string, error)
	Info(msg string)
	Error(msg string)
}

// Authenticator checks a user's credentials (PAM in production).
type Authenticator interface {
	Authenticate(username string, p Prompter) error
}

var (
	ErrBadLogin = errors.New("login failed")
	ErrNotAdmin = errors.New("this account isn't in the " + paths.AdminGroup + " group, so it can't use the manager")
)

// ThrottledError means too many recent failures for this username.
type ThrottledError struct{ Until time.Time }

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("too many failed logins; try again after %s", e.Until.Local().Format("15:04:05"))
}

// IdleTimeout is how long the menu waits before going back to the login screen.
const IdleTimeout = 10 * time.Minute

// Login ties the pieces together.
type Login struct {
	Auth     Authenticator
	Throttle *Throttle
	// IsAdmin defaults to the real group check.
	IsAdmin func(username string) (bool, error)
}

// Run logs username in: refuses throttled names, asks PAM, then checks the
// admins group. Group membership is checked after the password so the
// answer doesn't reveal which accounts are admins.
func (l *Login) Run(username string, p Prompter) error {
	if err := validate.Username(username); err != nil {
		return fmt.Errorf("%w: %v", ErrBadLogin, err)
	}
	if l.Throttle != nil {
		if err := l.Throttle.Check(username); err != nil {
			return err
		}
	}
	if err := l.Auth.Authenticate(username, p); err != nil {
		if l.Throttle != nil {
			l.Throttle.Fail(username)
		}
		return err
	}
	if l.Throttle != nil {
		l.Throttle.Succeed(username)
	}
	isAdmin := l.IsAdmin
	if isAdmin == nil {
		isAdmin = IsAdmin
	}
	ok, err := isAdmin(username)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotAdmin
	}
	return nil
}

// IsAdmin reports whether username is in the admins group, including as its
// primary group. With cgo on, os/user goes through NSS, so directory
// accounts work too.
func IsAdmin(username string) (bool, error) {
	u, err := user.Lookup(username)
	if err != nil {
		return false, fmt.Errorf("look up %s: %w", username, err)
	}
	g, err := user.LookupGroup(paths.AdminGroup)
	var unknown user.UnknownGroupError
	if errors.As(err, &unknown) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look up group %s: %w", paths.AdminGroup, err)
	}
	gids, err := u.GroupIds()
	if err != nil {
		return false, fmt.Errorf("groups of %s: %w", username, err)
	}
	return slices.Contains(gids, g.Gid), nil
}

// Session tracks the logged-in admin and when they last did anything.
type Session struct {
	User     string
	SSHUser  string // who ran sudo, when different from User
	lastSeen time.Time
}

func NewSession(user, sshUser string, now time.Time) *Session {
	return &Session{User: user, SSHUser: sshUser, lastSeen: now}
}

func (s *Session) Touch(now time.Time) { s.lastSeen = now }

// Idle reports whether the session should go back to the login screen.
func (s *Session) Idle(now time.Time) bool { return now.Sub(s.lastSeen) >= IdleTimeout }
