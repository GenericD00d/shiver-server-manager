package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

func settingsHelp() string {
	var b strings.Builder
	b.WriteString("Settings:\n")
	for _, s := range sharkord.Settings {
		fmt.Fprintf(&b, "  %-36s %s\n", strings.TrimPrefix(s.Name, "SHARKORD_"), s.Help)
	}
	return b.String()
}

func (a *App) tokenCmd() *cobra.Command {
	show := &cobra.Command{
		Use: "show <id>", Short: "Show a server's secret token", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				return a.logged("token show", args[0], func() error {
					if _, err := s.Instance(args[0]); err != nil {
						return err
					}
					k, err := a.unlockVault()
					if err != nil {
						return err
					}
					defer k.Forget()
					tok, err := s.Token(k, args[0])
					if err != nil {
						return err
					}
					if !a.term.isTerminal() {
						fmt.Fprintln(a.Out, tok)
						return nil
					}
					fmt.Fprintf(a.Out, "\nSecret token for %s:\n\n    %s\n\nAnyone with it can take over the server. Press Enter to hide it.", args[0], tok)
					_, _ = a.term.readLine()
					a.term.clearScreen()
					return nil
				})
			})
		},
	}
	save := &cobra.Command{
		Use: "save <id>", Short: "Check and save a server's secret token", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("token save", args[0], func() error {
					if _, err := s.Instance(args[0]); err != nil {
						return err
					}
					tok, err := a.term.passphrase("Secret token: ")
					if err != nil {
						return err
					}
					if err := s.SaveToken(args[0], tok); err != nil {
						return err
					}
					a.say("✓ The token matches the server's database and is saved in the vault.")
					return nil
				})
			})
		},
	}
	newTok := &cobra.Command{
		Use: "new <id>", Short: "Make a new secret token, replacing the old one", Args: cobra.ExactArgs(1),
		Long: "Makes a new secret token for a server and saves it in the vault. The old token stops working.\n\n" +
			"Sharkord also signs logins with it, so everyone on the server has to log in again. The server is backed up and restarted.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				if _, err := s.Instance(args[0]); err != nil {
					return err
				}
				a.say("A new secret token for %s: the old one stops working, and everyone on the server has to log in again. The server is backed up and restarted.", args[0])
				ok, err := a.term.confirm("Make a new token?")
				if err != nil || !ok {
					return err
				}
				return a.logged("token new", args[0], func() error {
					if err := s.NewToken(cmd.Context(), args[0]); err != nil {
						return err
					}
					a.say("✓ %s has a new secret token, saved in the vault. See it with 'token show %s'.", args[0], args[0])
					return nil
				})
			})
		},
	}
	c := &cobra.Command{Use: "token", Short: "Secret tokens (owner tokens)", Args: cobra.NoArgs}
	c.AddCommand(show, save, newTok)
	return c
}
