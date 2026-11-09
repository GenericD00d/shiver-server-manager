package cli

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

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
