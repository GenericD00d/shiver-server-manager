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
	c := &cobra.Command{Use: "token", Short: "Secret tokens (owner tokens)", Args: cobra.NoArgs}
	c.AddCommand(show, save)
	return c
}
