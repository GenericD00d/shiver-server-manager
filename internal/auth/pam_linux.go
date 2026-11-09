//go:build linux && cgo

package auth

import (
	"fmt"

	"github.com/msteinert/pam/v2"
)

// PAM checks logins through a PAM service file, so they follow the server's
// own rules, including two-factor modules and directory accounts.
type PAM struct {
	Service string
	ConfDir string // tests only; empty means /etc/pam.d
}

func (a PAM) Authenticate(username string, p Prompter) error {
	conv := pam.ConversationFunc(func(s pam.Style, msg string) (string, error) {
		switch s {
		case pam.PromptEchoOff:
			return p.Hidden(msg)
		case pam.PromptEchoOn:
			return p.Visible(msg)
		case pam.ErrorMsg:
			p.Error(msg)
			return "", nil
		case pam.TextInfo:
			p.Info(msg)
			return "", nil
		default:
			return "", fmt.Errorf("unsupported PAM message style %d", s)
		}
	})
	var t *pam.Transaction
	var err error
	if a.ConfDir != "" {
		t, err = pam.StartConfDir(a.Service, username, conv, a.ConfDir)
	} else {
		t, err = pam.Start(a.Service, username, conv)
	}
	if err != nil {
		return fmt.Errorf("start PAM: %w", err)
	}
	defer t.End()
	if err := t.Authenticate(pam.DisallowNullAuthtok); err != nil {
		return fmt.Errorf("%w: %v", ErrBadLogin, err)
	}
	if err := t.AcctMgmt(pam.DisallowNullAuthtok); err != nil {
		return fmt.Errorf("%w: %v", ErrBadLogin, err)
	}
	return nil
}
