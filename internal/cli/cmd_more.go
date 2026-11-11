package cli

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

func (a *App) instanceDomain() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "domain <id> <new domain>",
		Short: "Move a server to a new domain",
		Long:  "Sets up the new domain and its certificate first, then removes the old site and certificate. Members need the new address; their accounts stay.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				inst, err := s.Instance(args[0])
				if err != nil {
					return err
				}
				old := inst.Domain
				return a.logged("instance domain", args[0], func() error {
					if err := s.ChangeDomain(cmd.Context(), args[0], args[1], force); err != nil {
						return err
					}
					a.say("✓ %s now answers at %s. Tell members the new address.", args[0], s.St.Instances[args[0]].Domain)
					if w := ops.DomainWarnings(s.St.Instances[args[0]], old); len(w) > 0 {
						a.warn("These settings still name %s: %s (change them with 'instance set')", old, strings.Join(w, ", "))
					}
					return nil
				})
			})
		},
	}
	c.Flags().BoolVar(&force, "force", false, "change even if the new domain doesn't point here yet")
	return c
}

func (a *App) instanceLimits() *cobra.Command {
	var memory, cpu int
	c := &cobra.Command{
		Use:   "limits <id>",
		Short: "Cap a server's memory and CPU, so one busy community can't slow the others",
		Long:  "--memory is in MiB (the server slows down at 90% of it and is restarted if it reaches it); --cpu is a percentage of one core (200 = two cores). 0 removes a cap. The server restarts.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("instance limits", args[0], func() error {
					if err := s.SetLimits(cmd.Context(), args[0], memory, cpu); err != nil {
						return err
					}
					a.say("✓ Limits set for %s.", args[0])
					return nil
				})
			})
		},
	}
	c.Flags().IntVar(&memory, "memory", 0, "memory cap in MiB (0 = none)")
	c.Flags().IntVar(&cpu, "cpu", 0, "CPU cap, percent of one core (0 = none)")
	return c
}

func (a *App) removeEverythingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove-everything",
		Short: "Delete every server, before uninstalling the manager",
		Long:  "Deletes every server and its data for good, and stops the manager's timers. The vault, backups and records stay, so the package's own removal never touches communities.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				a.say(red(a, "This deletes every server (%s) and all their data, for good."), strings.Join(s.IDs(), ", "))
				typed, err := a.term.visible("Type \"remove everything\" to go ahead: ", !a.term.isTerminal())
				if err != nil {
					return err
				}
				if strings.TrimSpace(typed) != "remove everything" {
					return errors.New("not confirmed; nothing was deleted")
				}
				return a.logged("remove everything", "", func() error {
					if err := s.RemoveEverything(cmd.Context()); err != nil {
						return err
					}
					osInfo, _ := ops.OS()
					a.say("✓ Every server is deleted. Remove the package with: %s", osInfo.UninstallCommand(paths.Cmd))
					a.say("Kept: the vault and records in %s, backups in %s.", paths.StateDir, s.St.Settings.BackupLocation)
					return nil
				})
			})
		},
	}
}
