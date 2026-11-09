package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

func (a *App) deleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a server and all its data, for good",
		Long:  "Deletes the server's service, site, certificate, firewall rules, data folder (its database, uploads and backups Sharkord made), secret token and system user. The community is gone for good. Export it first if you might want it back.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				inst, err := s.Instance(id)
				if err != nil {
					return err
				}
				if inst.Operation == nil || inst.Operation.Name != ops.OpDelete {
					a.say(red(a, "Deleting %s erases its community at %s for good: messages, uploads, accounts and settings."), id, inst.Domain)
					a.say("Export it first if you might want it back: %s export %s --out <file>", "shiver-server-manager", id)
					// The ID is typed back even with --yes: it's the one confirmation
					// a script must state explicitly.
					typed, err := a.term.visible(fmt.Sprintf("Type the server ID (%s) to delete it: ", id), !a.term.isTerminal())
					if err != nil {
						return err
					}
					if strings.TrimSpace(typed) != id {
						return errors.New("the ID didn't match; nothing was deleted")
					}
				}
				return a.logged("instance delete", id, func() error {
					if err := s.Delete(cmd.Context(), id); err != nil {
						return err
					}
					a.say("Server %s is deleted.", id)
					return nil
				})
			})
		},
	}
}

// red marks destructive confirmations, unless colour is off.
func red(a *App, format string) string {
	if a.Env.NoColor || a.Globals.JSON || !a.term.isTerminal() {
		return format
	}
	return "\033[31m" + format + "\033[0m"
}

func (a *App) repairCmd() *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:   "repair",
		Short: "Make the system match the records",
		Long:  "Finishes interrupted creates and deletes, redoes anything missing for each server (files, service, firewall rules, site files), and offers to remove leftovers that belong to no server. Data folders are only removed when you say yes to each one.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.runRepair(cmd.Context(), s, dryRun, nil)
			})
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "only print what would change")
	return c
}

// runRepair applies a repair plan; filter keeps only some findings.
func (a *App) runRepair(ctx context.Context, s *ops.Sys, dryRun bool, filter func(ops.Finding) bool) error {
	plan, err := s.RepairPlan(ctx)
	if err != nil {
		return err
	}
	var kept []ops.Finding
	for _, f := range plan {
		if filter == nil || filter(f) {
			kept = append(kept, f)
		}
	}
	if a.Globals.JSON && dryRun {
		if kept == nil {
			kept = []ops.Finding{}
		}
		return json.NewEncoder(a.Out).Encode(kept)
	}
	if len(kept) == 0 {
		a.say("✓ Everything matches the records.")
		return nil
	}
	var errs []error
	for _, f := range kept {
		label := f.What
		if f.Instance != "" {
			label = f.Instance + ": " + f.What
		}
		if dryRun || f.Fix == nil {
			prefix := "would " + f.Kind
			if f.Fix == nil {
				prefix = "problem"
			}
			a.say("  %s: %s", prefix, label)
			continue
		}
		if f.Ask {
			var ok bool
			if f.Data {
				// Never with --yes: only an explicit answer removes data.
				if !a.term.isTerminal() {
					a.say("  ! kept %s (data is only removed when you answer yes in a terminal)", label)
					continue
				}
				fmt.Fprintf(a.Err, "%s [y/N] ", red(a, "Remove "+label+"?"))
				ans, _ := a.term.readLine()
				ok = strings.EqualFold(strings.TrimSpace(ans), "y") || strings.EqualFold(strings.TrimSpace(ans), "yes")
			} else {
				ok, err = a.term.confirm("Remove " + label + "?")
				if err != nil {
					return err
				}
			}
			if !ok {
				a.say("  - kept %s", label)
				continue
			}
		}
		err := f.Fix(ctx)
		a.log.Action("repair", err, "instance", f.Instance, "what", f.What)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", label, err))
			a.say("  ✗ %s: %v", label, err)
			continue
		}
		a.say("  ✓ %s", label)
	}
	return errors.Join(errs...)
}

func (a *App) firewallRepairCmd() *cobra.Command {
	return &cobra.Command{
		Use: "repair", Short: "Add missing rules and remove leftovers", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.runRepair(cmd.Context(), s, false, func(f ops.Finding) bool {
					return strings.Contains(f.What, "firewall")
				})
			})
		},
	}
}
