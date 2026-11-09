package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// setupDirs are the manager's folders and their modes.
var setupDirs = []struct {
	path string
	mode os.FileMode
}{
	// 711: instance users must pass through to their version link in bin/
	// (the unit's ExecStart). Nothing in here is readable by them.
	{paths.StateDir, 0o711},
	{paths.VaultDir, 0o700},
	{paths.VersionLinkDir, 0o755},
	{paths.ConfigDir, 0o700},
	{paths.InstanceEnvDir, 0o700},
	{paths.DefaultDataLocation, 0o711},
	{paths.DefaultBackupLocation, 0o700},
}

func (a *App) setupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "First-time setup: folders, the admins group, the first admin and the vault",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			admins := osuser.Admins(a.runner)
			members, err := admins.Members(ctx)
			if err != nil {
				return err
			}
			// Once there are admins, only an admin may rerun setup. Before
			// that, whoever has root through sudo is setting the server up.
			if len(members) > 0 {
				if err := a.login(); err != nil {
					return err
				}
			} else {
				a.log.User = a.Env.SudoUser
			}
			lk, err := a.lock(state.Exclusive)
			if err != nil {
				return err
			}
			defer lk.Release()
			err = a.runSetup(cmd, admins, members)
			a.log.Action("setup", err)
			return err
		},
	}
}

func (a *App) runSetup(cmd *cobra.Command, admins *osuser.Group, members []string) error {
	ctx := cmd.Context()
	for _, d := range setupDirs {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return err
		}
		if err := os.Chmod(d.path, d.mode); err != nil {
			return err
		}
	}
	a.say("✓ Folders")

	if err := admins.Ensure(ctx); err != nil {
		return err
	}
	a.say("✓ Group %s", paths.AdminGroup)

	if a.needsLogin() {
		if err := admins.Add(ctx, a.Env.SudoUser); err != nil {
			return err
		}
		if !slices.Contains(members, a.Env.SudoUser) {
			a.log.Action("admins add", nil, "admin", a.Env.SudoUser)
		}
		a.say("✓ %s is an admin", a.Env.SudoUser)
	} else if len(members) == 0 {
		a.say("! No admins yet. Add one with: %s admins add <user>", paths.Cmd)
	}

	if _, err := os.Stat(paths.StateFile); errors.Is(err, os.ErrNotExist) {
		if err := state.Save(paths.StateFile, state.Default()); err != nil {
			return err
		}
	}
	a.say("✓ Records")

	v := a.vault()
	if v.Exists() {
		a.say("✓ Vault (already set up)")
		return nil
	}
	if err := a.createVault(v); err != nil {
		return err
	}
	a.say("✓ Vault")
	return nil
}

func (a *App) createVault(v *vault.Vault) error {
	fmt.Fprintln(a.Err, "The vault keeps secret tokens and data-folder keys. Choose a passphrase for it;\nit's separate from your OS password.")
	p, err := a.term.newPassphrase("vault passphrase")
	if err != nil {
		return err
	}
	key, err := v.Create(p)
	if err != nil {
		return err
	}
	a.log.Action("vault create", nil)
	a.term.secrets.AddSecret([]byte(key))

	fmt.Fprintf(a.Out, "\nRecovery key:\n\n    %s\n\n", key)
	fmt.Fprintln(a.Out, "This is shown once. Write it down or keep it in a password manager, away from\nthis server. It's the only way back into the vault, and into every backup, if\nthe passphrase is lost.")
	for {
		typed, err := a.term.visible("\nType the last 6 characters of the recovery key to confirm you've saved it: ", a.Globals.PassphraseStdin)
		if err != nil {
			return fmt.Errorf("the vault was created, but the recovery key wasn't confirmed: %w", err)
		}
		if vault.ConfirmRecoveryKey(key, typed) {
			break
		}
		if a.Globals.PassphraseStdin {
			return errors.New("the vault was created, but the recovery key confirmation didn't match")
		}
		fmt.Fprintln(a.Err, "That doesn't match. Check what you saved.")
	}
	a.term.clearScreen()
	return nil
}

func (a *App) vaultCmd() *cobra.Command {
	unlock := &cobra.Command{
		Use:   "unlock",
		Short: "Unlock the vault",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.login(); err != nil {
				return err
			}
			k, err := a.unlockVault()
			if err != nil {
				return err
			}
			defer k.Forget()
			a.say("The vault is unlocked.")
			return nil
		},
	}
	change := &cobra.Command{
		Use:   "change-passphrase",
		Short: "Change the vault passphrase",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.login(); err != nil {
				return err
			}
			lk, err := a.lock(state.Exclusive)
			if err != nil {
				return err
			}
			defer lk.Release()
			k, err := a.unlockVault()
			if err != nil {
				return err
			}
			defer k.Forget()
			p, err := a.term.newPassphrase("vault passphrase")
			if err != nil {
				return err
			}
			err = a.vault().ChangePassphrase(k, p)
			a.log.Action("vault change-passphrase", err)
			if err == nil {
				a.say("The vault passphrase is changed. The recovery key stays the same.")
			}
			return err
		},
	}
	recoverCmd := &cobra.Command{
		Use:   "recover",
		Short: "Open the vault with the recovery key and set a new passphrase",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if err := a.login(); err != nil {
				return err
			}
			lk, err := a.lock(state.Exclusive)
			if err != nil {
				return err
			}
			defer lk.Release()
			v := a.vault()
			if !v.Exists() {
				return vault.ErrNoVault
			}
			rk, err := a.term.passphrase("Recovery key (AGE-SECRET-KEY-1...): ")
			if err != nil {
				return err
			}
			if _, err := vault.ParseRecoveryKey(rk); err != nil {
				return err
			}
			p, err := a.term.newPassphrase("vault passphrase")
			if err != nil {
				return err
			}
			k, err := v.Recover(rk, p)
			a.log.Action("vault recover", err)
			if err != nil {
				return err
			}
			k.Forget()
			a.say("The vault is open again with the new passphrase. The recovery key stays the same.")
			return nil
		},
	}
	c := &cobra.Command{Use: "vault", Short: "The vault for secrets", Args: cobra.NoArgs}
	c.AddCommand(unlock, change, recoverCmd)
	return c
}

func (a *App) adminsCmd() *cobra.Command {
	list := &cobra.Command{
		Use:   "list",
		Short: "List admins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.login(); err != nil {
				return err
			}
			lk, err := a.lock(state.Shared)
			if err != nil {
				return err
			}
			defer lk.Release()
			m, err := osuser.Admins(a.runner).Members(cmd.Context())
			if err != nil {
				return err
			}
			if a.Globals.JSON {
				if m == nil {
					m = []string{}
				}
				return json.NewEncoder(a.Out).Encode(map[string]any{"admins": m})
			}
			for _, u := range m {
				fmt.Fprintln(a.Out, u)
			}
			return nil
		},
	}
	add := &cobra.Command{
		Use:   "add <user>",
		Short: "Add an existing OS account as an admin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.changeAdmins(cmd, "admins add", args[0], (*osuser.Group).Add,
				"%s is now an admin. They start the manager with: sudo "+paths.Cmd)
		},
	}
	remove := &cobra.Command{
		Use:   "remove <user>",
		Short: "Remove an admin",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.changeAdmins(cmd, "admins remove", args[0], (*osuser.Group).Remove,
				"%s is no longer an admin.")
		},
	}
	c := &cobra.Command{Use: "admins", Short: "Who can use the manager", Args: cobra.NoArgs}
	c.AddCommand(list, add, remove)
	return c
}

func (a *App) changeAdmins(cmd *cobra.Command, action, user string, fn func(*osuser.Group, context.Context, string) error, done string) error {
	if err := a.login(); err != nil {
		return err
	}
	lk, err := a.lock(state.Exclusive)
	if err != nil {
		return err
	}
	defer lk.Release()
	err = fn(osuser.Admins(a.runner), cmd.Context(), user)
	a.log.Action(action, err, "admin", user)
	if err == nil {
		a.say(done, user)
	}
	return err
}
