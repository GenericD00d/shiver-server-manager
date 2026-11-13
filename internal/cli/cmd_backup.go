package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/backup"
	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

func (a *App) backupCmd() *cobra.Command {
	var all, unattended, scheduled bool
	c := &cobra.Command{
		Use:   "backup [<id>]",
		Short: "Back up one server, or all of them",
		Long: "Backups are encrypted to the vault's public key, so making one needs no passphrase; restoring needs the vault passphrase or the recovery key. The server keeps running. " +
			"Each server's schedule and how many to keep: backup schedule <id>.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if scheduled {
				if all || len(args) > 0 {
					return usageErr("--scheduled takes no server ID")
				}
				all = true
			} else if all == (len(args) == 1) {
				return usageErr("give one server ID, or --all")
			}
			if unattended && a.needsLogin() {
				return usageErr("--unattended is for the timer, which runs as root")
			}
			if scheduled {
				// The timer runs every 5 minutes: look without the lock first,
				// so it only waits for one when a backup is owed.
				st, err := state.Load(paths.StateFile)
				if err != nil {
					return err
				}
				if len(ops.New(a.runner, st, a.log, paths.Cmd+"/"+a.Version).DueBackups()) == 0 {
					return nil
				}
			}
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				ids := args
				if scheduled {
					ids = s.DueBackups()
				} else if all {
					ids = s.IDs()
				}
				var errs []error
				for _, id := range ids {
					if all {
						if inst := s.St.Instances[id]; inst.Operation != nil {
							a.say("  - %s: skipped (unfinished %s)", id, inst.Operation.Name)
							continue
						}
						if inst := s.St.Instances[id]; inst.Encrypted && !s.LUKS.IsMounted(cmd.Context(), inst.DataPath) {
							a.say("  - %s: skipped (locked; run 'vault unlock')", id)
							continue
						}
					}
					var file string
					err := a.logged("backup", id, func() (err error) {
						file, err = s.Backup(cmd.Context(), id)
						return err
					})
					if err != nil {
						errs = append(errs, fmt.Errorf("%s: %w", id, err))
						a.say("  ✗ %s: %v", id, err)
						continue
					}
					a.say("  ✓ %s -> %s", id, file)
				}
				return errors.Join(errs...)
			})
		},
	}
	c.Flags().BoolVar(&all, "all", false, "back up every server")
	c.Flags().BoolVar(&unattended, "unattended", false, "run from the timer: no login, no prompts")
	c.Flags().BoolVar(&scheduled, "scheduled", false, "back up the servers whose schedule owes a backup (the timer)")
	_ = c.Flags().MarkHidden("scheduled")

	list := &cobra.Command{
		Use: "list [<id>]", Short: "List a server's backups", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				ids := args
				if len(ids) == 0 {
					ids = s.IDs()
				}
				out := map[string][]backup.Entry{}
				for _, id := range ids {
					list, err := s.Backups(id)
					if err != nil {
						return err
					}
					out[id] = list
				}
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(out)
				}
				w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "SERVER\tTAKEN\tSIZE\tFILE")
				sort.Strings(ids)
				for _, id := range ids {
					for _, e := range out[id] {
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", id, e.CreatedAt.Local().Format("2006-01-02 15:04"), gibStr(uint64(e.Size)), filepath.Join(s.BackupDir(id), e.File))
					}
				}
				return w.Flush()
			})
		},
	}
	c.AddCommand(list, a.backupScheduleCmd(), a.backupDeleteCmd())
	return c
}

func (a *App) restoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <id> <file>",
		Short: "Restore a server from a backup or export",
		Long:  "Unpacks the archive beside the current data, swaps it in while the server is stopped, and swaps back if it doesn't start. Everything since the backup is lost.",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, file := args[0], args[1]
			file, err := filepath.Abs(file)
			if err != nil {
				return err
			}
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				if _, err := s.Instance(id); err != nil {
					return err
				}
				identity, err := s.IdentityFor(file, func() (string, error) { return a.term.passphrase("Export passphrase: ") })
				if errors.Is(err, ops.ErrVaultLocked) {
					if s.VaultKey, err = a.unlockVault(); err != nil {
						return err
					}
					defer s.Forget()
					identity, err = s.IdentityFor(file, nil)
				}
				if err != nil {
					return err
				}
				a.say(red(a, "Restoring replaces %s's data with the archive's; anything since it was made is lost."), id)
				ok, err := a.term.confirm("Restore?")
				if err != nil || !ok {
					return err
				}
				return a.logged("restore", id, func() error {
					old, err := s.Restore(cmd.Context(), id, file, identity)
					if err != nil {
						return err
					}
					a.say("✓ %s is restored and running.", id)
					return a.offerDeleteLeftover(s, id, old)
				})
			})
		},
	}
}

func (a *App) offerDeleteLeftover(s *ops.Sys, id, dir string) error {
	if dir == "" {
		return nil
	}
	a.say("The previous data is in %s.", dir)
	if !a.term.isTerminal() && !a.Globals.Yes {
		a.say("Delete it once you've checked the server, with: rm -r %s", dir)
		return nil
	}
	ok, err := a.term.confirm("Delete the previous data now?")
	if err != nil || !ok {
		return err
	}
	return s.DeleteLeftover(id, dir)
}

func (a *App) exportCmd() *cobra.Command {
	var out string
	var withToken bool
	c := &cobra.Command{
		Use:   "export <id>",
		Short: "Export a server to an encrypted file, to move it to another server",
		Long:  "The export is encrypted with a passphrase you choose for it, so it can move without sharing the vault. The server keeps running. The secret token goes inside only with --include-token.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				if _, err := s.Instance(args[0]); err != nil {
					return err
				}
				if withToken {
					var err error
					if s.VaultKey, err = a.unlockVault(); err != nil {
						return err
					}
					defer s.Forget()
				}
				p, err := a.term.newPassphrase("export passphrase")
				if err != nil {
					return err
				}
				return a.logged("export", args[0], func() error {
					if err := s.Export(cmd.Context(), args[0], out, p, withToken); err != nil {
						return err
					}
					a.say("✓ Exported %s to %s. Keep the passphrase with it; without it the file can't be opened.", args[0], out)
					return nil
				})
			})
		},
	}
	c.Flags().StringVar(&out, "out", "", "the file to write")
	c.Flags().BoolVar(&withToken, "include-token", false, "include the secret token inside the encrypted file")
	_ = c.MarkFlagRequired("out")
	return c
}

func (a *App) importCmd() *cobra.Command {
	var r ops.CreateRequest
	var tls, version string
	var noEncrypt bool
	c := &cobra.Command{
		Use:   "import <file|folder|docker:container>",
		Short: "Import a server from an export or backup, a Sharkord data folder, or a Docker container",
		Long: "Sources:\n  an export or backup file   asks for the export's passphrase, or unlocks the vault for a backup\n" +
			"  a data folder              a plain install's ~/.config/sharkord (or its SHARKORD_DATA_PATH)\n" +
			"  docker:<container>         the volume mounted at /home/bun/.config/sharkord\n\n" +
			"A server that's still running is copied while it runs, then you're asked to stop it for a short final copy.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r.TLS = state.TLS(tls)
			r.Encrypted = !noEncrypt
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				defer s.Forget()
				src, err := a.importSource(cmd, s, args[0])
				if err != nil {
					return err
				}
				plan, err := s.PlanImport(cmd.Context(), src, version)
				if err != nil {
					return err
				}
				if plan.DataVersion != "" {
					a.say("The data was last run by Sharkord %s; it will run on %s.", plan.DataVersion, plan.RunVersion)
				}
				if len(plan.Settings) > 0 {
					keys := make([]string, 0, len(plan.Settings))
					for k := range plan.Settings {
						keys = append(keys, k)
					}
					sort.Strings(keys)
					a.say("Settings carried over (ports, paths and addresses are the manager's own):")
					for _, k := range keys {
						v := plan.Settings[k]
						if strings.Contains(k, "SECRET") {
							v = "(hidden)"
						}
						a.say("  %s=%s", k, v)
					}
					ok, err := a.term.confirm("Carry these settings over?")
					if err != nil {
						return err
					}
					if !ok {
						plan.Settings = nil
					}
				}
				hooks := ops.ImportHooks{
					StopSource: func(who string) error { return a.askStopSource(cmd, src, who) },
					Token: func() (string, error) {
						return a.term.passphrase("Secret token of the old server (Enter to skip): ")
					},
				}
				return a.logged("import", r.ID, func() error {
					if err := s.Import(cmd.Context(), ops.ImportRequest{CreateRequest: r, Source: src, Plan: plan}, hooks); err != nil {
						return err
					}
					a.say("✓ %s is imported and running. Point %s at this server (see Help, \"Point a domain at the server\"); members keep their accounts.", r.ID, r.Domain)
					return nil
				})
			})
		},
	}
	f := c.Flags()
	f.StringVar(&r.ID, "id", "", "the new server's ID")
	f.StringVar(&r.Domain, "domain", "", "the new server's domain")
	f.StringVar(&r.Location, "location", "", "storage location (default: the default location)")
	f.StringVar(&tls, "tls", "", "certificate type (default: the export's, or letsencrypt)")
	f.StringVar(&version, "version", "", "Sharkord version to run it on (default: the newest release)")
	f.BoolVar(&noEncrypt, "no-encrypt", false, "don't put the data folder in an encrypted container")
	f.BoolVar(&r.SkipDNSCheck, "skip-dns-check", false, "don't check the domain points here (it points at a proxy in front of the server)")
	_ = c.MarkFlagRequired("id")
	_ = c.MarkFlagRequired("domain")
	return c
}

func (a *App) importSource(cmd *cobra.Command, s *ops.Sys, arg string) (ops.ImportSource, error) {
	if name, ok := strings.CutPrefix(arg, "docker:"); ok {
		dir, err := ops.DockerVolume(cmd.Context(), a.runner, name)
		if err != nil {
			return ops.ImportSource{}, err
		}
		a.say("Docker container %s keeps its data in %s.", name, dir)
		return ops.ImportSource{Folder: dir, Docker: name}, nil
	}
	p, err := filepath.Abs(arg)
	if err != nil {
		return ops.ImportSource{}, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return ops.ImportSource{}, err
	}
	if fi.IsDir() {
		return ops.ImportSource{Folder: p}, nil
	}
	identity, err := s.IdentityFor(p, func() (string, error) { return a.term.passphrase("Export passphrase: ") })
	if errors.Is(err, ops.ErrVaultLocked) {
		if s.VaultKey, err = a.unlockVault(); err != nil {
			return ops.ImportSource{}, err
		}
		identity, err = s.IdentityFor(p, nil)
	}
	return ops.ImportSource{Archive: p, Identity: identity}, err
}

// askStopSource waits for the old server to be stopped for the final copy.
func (a *App) askStopSource(cmd *cobra.Command, src ops.ImportSource, who string) error {
	if src.Docker != "" {
		ok, err := a.term.confirm(fmt.Sprintf("The first copy is done. Stop Docker container %s now for the final copy?", src.Docker))
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("the old server must be stopped for the final copy; run the import again when it is")
		}
		res := exec.CommandContext(cmd.Context(), "/usr/bin/docker", "stop", src.Docker)
		if out, err := res.CombinedOutput(); err != nil {
			return fmt.Errorf("docker stop: %v: %s", err, out)
		}
		return nil
	}
	if !a.term.isTerminal() {
		return fmt.Errorf("the old server is still running (%s); stop it and run the import again", who)
	}
	a.say("The first copy is done. The old server is still running (%s). Stop it now, then press Enter for the final copy.", who)
	_, err := a.term.readLine()
	return err
}

func (a *App) instanceUpdate() *cobra.Command {
	var version string
	c := &cobra.Command{
		Use:   "update <id>",
		Short: "Update a server to a newer Sharkord release",
		Long:  "Backs up the server, switches it to the new version and restarts it. If it doesn't answer within 90 seconds, it goes back to the old version with the database from that backup.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				var err error
				if s.VaultKey, err = a.unlockVault(); err != nil {
					return err
				}
				defer s.Forget()
				return a.logged("instance update", args[0], func() error { return s.Update(cmd.Context(), args[0], version) })
			})
		},
	}
	c.Flags().StringVar(&version, "version", "", "Sharkord version (default: the newest release)")
	return c
}

// internalVacuumCmd is run by the manager itself, as an instance's user,
// to copy its live database (see ops.runSelfAs). It refuses root.
func (a *App) internalVacuumCmd() *cobra.Command {
	return &cobra.Command{
		Use: "internal-vacuum <src> <dst>", Hidden: true, Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := refuseRoot(); err != nil {
				return err
			}
			return backup.Vacuum(args[0], args[1])
		},
	}
}
