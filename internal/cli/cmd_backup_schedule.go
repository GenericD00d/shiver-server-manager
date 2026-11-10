package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/backup"
	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

func (a *App) backupScheduleCmd() *cobra.Command {
	var on, off bool
	var times, days, keep string
	var keepDays int
	c := &cobra.Command{
		Use:   "schedule [<id>]",
		Short: "Show or change a server's backup schedule and how many backups it keeps",
		Long: "Without options, shows every server's schedule (or one server's). Times are local, HH:MM, comma-separated; days are mon..sun or \"every\". " +
			"--keep is how many backups to keep (a number, \"all\" or \"default\"); --keep-days also drops backups older than that many days (0 for no age limit). " +
			"The newest backup is never dropped for its age. A tighter limit deletes the extra backups at once.\n\n" +
			"Examples:\n  backup schedule chat-a --times 03:30 --days mon,thu --keep 8\n  backup schedule chat-a --keep-days 30 --keep all\n  backup schedule chat-a --off\n  backup schedule chat-a --times default --days every --keep default",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			change := on || off || cmd.Flags().Changed("times") || cmd.Flags().Changed("days") || cmd.Flags().Changed("keep") || cmd.Flags().Changed("keep-days")
			if !change {
				return a.withOps(state.Shared, func(s *ops.Sys) error {
					ids := args
					if len(ids) == 0 {
						ids = s.IDs()
					}
					out := []ops.BackupPolicyView{}
					for _, id := range ids {
						v, err := s.BackupPolicy(id)
						if err != nil {
							return err
						}
						out = append(out, v)
					}
					if a.Globals.JSON {
						return json.NewEncoder(a.Out).Encode(out)
					}
					w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
					fmt.Fprintln(w, "SERVER\tSCHEDULE\tKEEP\tNEXT\tBACKUPS")
					for _, v := range out {
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\n", v.Instance, describeSchedule(v), describeKeep(v), whenStr(v.Next), v.Backups)
					}
					return w.Flush()
				})
			}
			if len(args) != 1 {
				return usageErr("give the server ID to change")
			}
			if on && off {
				return usageErr("--on or --off, not both")
			}
			id := args[0]
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				inst, err := s.Instance(id)
				if err != nil {
					return err
				}
				p := inst.Backups
				p.Times = append([]string(nil), p.Times...)
				p.Days = append([]string(nil), p.Days...)
				if on || off {
					p.Off = off
				}
				if cmd.Flags().Changed("times") {
					if strings.TrimSpace(times) == "default" {
						p.Times = nil
					} else if p.Times, err = backup.ParseTimes(times); err != nil {
						return usageErr("--times: %v", err)
					}
				}
				if cmd.Flags().Changed("days") {
					if p.Days, err = backup.ParseDays(days); err != nil {
						return usageErr("--days: %v", err)
					}
				}
				if cmd.Flags().Changed("keep") {
					switch k := strings.TrimSpace(keep); k {
					case "default":
						p.KeepCount = 0
					case "all":
						p.KeepCount = -1
					default:
						n, err := strconv.Atoi(k)
						if err != nil || n < 1 || n > 10000 {
							return usageErr("--keep: a number from 1, \"all\" or \"default\"")
						}
						p.KeepCount = n
					}
				}
				if cmd.Flags().Changed("keep-days") {
					if keepDays < 0 || keepDays > 36500 {
						return usageErr("--keep-days: 0 or more")
					}
					p.KeepDays = keepDays
				}
				drop, err := s.PlanBackupPolicy(id, p)
				if err != nil {
					return err
				}
				if len(drop) > 0 {
					a.say(red(a, "This deletes %d of %s's backups now, the oldest first:"), len(drop), id)
					for _, f := range drop {
						a.say("  %s", f)
					}
					ok, err := a.term.confirm("Save and delete them?")
					if err != nil || !ok {
						return err
					}
				}
				return a.logged("backup schedule", id, func() error {
					removed, err := s.SetBackupPolicy(id, p)
					if err != nil {
						return err
					}
					v, _ := s.BackupPolicy(id)
					a.say("✓ %s: %s, keeping %s.", id, describeSchedule(v), describeKeep(v))
					if len(removed) > 0 {
						a.say("  Deleted %d old backups.", len(removed))
					}
					return nil
				})
			})
		},
	}
	f := c.Flags()
	f.BoolVar(&on, "on", false, "back this server up on its schedule")
	f.BoolVar(&off, "off", false, "stop scheduled backups for this server (its backups stay)")
	f.StringVar(&times, "times", "", `times of day, like "00:00,12:00", or "default"`)
	f.StringVar(&days, "days", "", `days, like "mon,thu", or "every"`)
	f.StringVar(&keep, "keep", "", `how many to keep: a number, "all" or "default"`)
	f.IntVar(&keepDays, "keep-days", 0, "also drop backups older than this many days (0: no age limit)")
	return c
}

func describeSchedule(v ops.BackupPolicyView) string {
	if v.Off {
		return "off"
	}
	days := "every day"
	if len(v.Days) > 0 {
		days = strings.Join(v.Days, ",")
	}
	return strings.Join(v.Times, ",") + " " + days
}

func describeKeep(v ops.BackupPolicyView) string {
	var parts []string
	if v.KeepCount > 0 {
		parts = append(parts, fmt.Sprintf("the last %d", v.KeepCount))
	}
	if v.KeepDays > 0 {
		parts = append(parts, fmt.Sprintf("%d days", v.KeepDays))
	}
	if len(parts) == 0 {
		return "all"
	}
	return strings.Join(parts, ", at most ")
}

func whenStr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func (a *App) backupDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id> <file>...",
		Short: "Delete backups",
		Long:  "Takes backup file names as backup list shows them (the name alone or the full path). Deleted backups can't be restored.",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				if _, err := s.Instance(id); err != nil {
					return err
				}
				var files []string
				for _, f := range args[1:] {
					if filepath.IsAbs(f) {
						if filepath.Dir(filepath.Clean(f)) != filepath.Clean(s.BackupDir(id)) {
							return fmt.Errorf("%s isn't one of %s's backups (%s)", f, id, s.BackupDir(id))
						}
						f = filepath.Base(f)
					}
					files = append(files, f)
				}
				a.say(red(a, "Deleting %d of %s's backups for good."), len(files), id)
				ok, err := a.term.confirm("Delete?")
				if err != nil || !ok {
					return err
				}
				return a.logged("backup delete", id, func() error {
					for _, f := range files {
						if err := s.DeleteBackup(id, f); err != nil {
							return err
						}
						a.say("  ✓ deleted %s", f)
					}
					return nil
				})
			})
		},
	}
}
