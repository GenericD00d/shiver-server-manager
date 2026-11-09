package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/auth"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/validate"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// needsLogin: root itself (no SUDO_USER, as the timers run) skips the login;
// anyone arriving through sudo logs in.
func (a *App) needsLogin() bool {
	return a.Env.SudoUser != "" && a.Env.SudoUser != "root"
}

// login checks the sudo user's password through PAM and their membership of
// the admins group, once per run.
func (a *App) login() error {
	if a.session != nil {
		return nil
	}
	if !a.needsLogin() {
		who := "root"
		if a.Globals.ActingUser != "" && validate.Username(a.Globals.ActingUser) == nil {
			who = a.Globals.ActingUser + " (web)"
		}
		a.session = auth.NewSession(who, "", time.Now())
		a.log.User = who
		return nil
	}
	username := a.Env.SudoUser
	l := &auth.Login{
		Auth:     auth.PAM{Service: paths.PAMService},
		Throttle: &auth.Throttle{Path: paths.LoginsFile},
	}
	if !a.Globals.PasswordStdin {
		fmt.Fprintf(a.Err, "Log in as %s.\n", username)
	}
	err := l.Run(username, pamPrompter{a.term})
	a.log.User = username
	a.log.SSHUser = username
	a.log.Action("login", err)
	if err != nil {
		return err
	}
	a.session = auth.NewSession(username, username, time.Now())
	return nil
}

// lock takes the manager's lock: Exclusive for anything that changes the
// system, Shared for reading.
func (a *App) lock(mode state.LockMode) (*state.Lock, error) {
	who := "root"
	if a.session != nil {
		who = a.session.User
	}
	return state.Acquire(paths.LockFile, mode, state.Holder(os.Getpid(), who), state.LockTimeout)
}

func (a *App) vault() *vault.Vault { return &vault.Vault{Dir: paths.VaultDir} }

// unlockVault asks for the vault passphrase and opens the vault.
func (a *App) unlockVault() (*vault.Key, error) {
	v := a.vault()
	if !v.Exists() {
		return nil, vault.ErrNoVault
	}
	p, err := a.term.passphrase("Vault passphrase: ")
	if err != nil {
		return nil, err
	}
	k, err := v.Unlock(p)
	a.log.Action("vault unlock", err)
	return k, err
}

// say prints a progress line, unless --json is on.
func (a *App) say(format string, args ...any) {
	if !a.Globals.JSON {
		fmt.Fprintf(a.Out, format+"\n", args...)
	}
}
