package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/storage"
)

func gibStr(b uint64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }

func (a *App) storageCmd() *cobra.Command {
	list := &cobra.Command{
		Use: "list", Short: "List storage locations with their drive and free space", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				locs := s.Locations(cmd.Context())
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(map[string]any{"locations": locs, "backupLocation": s.St.Settings.BackupLocation})
				}
				w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "LOCATION\tDRIVE\tFILE SYSTEM\tFREE\tSERVERS\t")
				for _, l := range locs {
					name := l.Path
					if l.Default {
						name += " (default)"
					}
					servers := strings.Join(l.Servers, ", ")
					if servers == "" {
						servers = "-"
					}
					extra := ""
					if l.Problem != "" {
						extra = "! " + l.Problem
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", name, l.Mount.Source, l.Mount.FSType, gibStr(l.FreeBytes), servers, extra)
				}
				w.Flush()
				fmt.Fprintf(a.Out, "\nBackups and exports go to %s\n", s.St.Settings.BackupLocation)
				return nil
			})
		},
	}
	add := &cobra.Command{
		Use: "add <path>", Short: "Add a folder as a storage location", Args: cobra.ExactArgs(1),
		Long: "The folder's drive must be ext4, XFS or Btrfs, not mounted noexec, and listed in /etc/fstab (or be the system drive), so it comes back after a reboot.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("storage add", "", func() error { return a.addLocation(cmd, s, args[0]) })
			})
		},
	}
	mount := &cobra.Command{
		Use: "mount [<device>]", Short: "Mount a formatted drive permanently and add a location on it", Args: cobra.MaximumNArgs(1),
		Long: "With no device, lists formatted drives that aren't mounted. Formatting a new drive is a step outside the manager; see Help, \"Add a new drive for Sharkord data\".",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				devs, err := storage.Unmounted(cmd.Context(), a.runner)
				if err != nil {
					return err
				}
				if len(args) == 0 {
					if len(devs) == 0 {
						a.say("No formatted, unmounted ext4, XFS or Btrfs drives found. See Help, \"Add a new drive for Sharkord data\".")
						return nil
					}
					for _, d := range devs {
						a.say("%s  %s  %s  label %q  -> would mount at %s", d.Path, d.Size, d.FSType, d.Label, storage.MountPointFor(d))
					}
					return nil
				}
				var dev *storage.Device
				for i := range devs {
					if devs[i].Path == args[0] {
						dev = &devs[i]
					}
				}
				if dev == nil {
					return fmt.Errorf("%s isn't a formatted, unmounted ext4, XFS or Btrfs drive; run 'storage mount' to list them", args[0])
				}
				target := storage.MountPointFor(*dev)
				a.say("This adds %s to /etc/fstab (with nofail, so a missing drive can't stop the server booting) and mounts it at %s.", dev.Path, target)
				ok, err := a.term.confirm("Go ahead?")
				if err != nil || !ok {
					return err
				}
				return a.logged("storage mount", "", func() error {
					mp, err := storage.MountDrive(cmd.Context(), a.runner, *dev, s.Fstab)
					if err != nil {
						return err
					}
					a.say("✓ Mounted at %s", mp)
					return a.addLocation(cmd, s, mp+"/sharkord")
				})
			})
		},
	}
	remove := &cobra.Command{
		Use: "remove <path>", Short: "Remove an unused storage location (its folder stays)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("storage remove", "", func() error { return s.RemoveLocation(args[0]) })
			})
		},
	}
	def := &cobra.Command{
		Use: "default <path>", Short: "Make a location the default for new servers", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("storage default", "", func() error { return s.SetDefaultLocation(args[0]) })
			})
		},
	}
	backups := &cobra.Command{
		Use: "backup-location <path>", Short: "Choose where backups and exports go", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("storage backup-location", "", func() error {
					if err := s.SetBackupLocation(cmd.Context(), args[0]); err != nil {
						return err
					}
					a.say("✓ Backups and exports now go to %s", s.St.Settings.BackupLocation)
					return nil
				})
			})
		},
	}
	c := &cobra.Command{Use: "storage", Short: "Storage locations", Args: cobra.NoArgs}
	c.AddCommand(list, add, mount, remove, def, backups)
	return c
}

func (a *App) addLocation(cmd *cobra.Command, s *ops.Sys, path string) error {
	blocked, err := s.AddLocation(cmd.Context(), path)
	if err != nil {
		return err
	}
	a.say("✓ %s is a storage location", path)
	if len(blocked) > 0 {
		a.say("Server users can't pass through %s (no x permission for others), so they couldn't reach their data.", strings.Join(blocked, ", "))
		ok, err := a.term.confirm("Fix it with chmod o+x?")
		if err != nil {
			return err
		}
		if ok {
			for _, p := range blocked {
				fi, err := os.Stat(p)
				if err != nil {
					return err
				}
				if err := os.Chmod(p, fi.Mode().Perm()|0o001); err != nil {
					return err
				}
			}
			a.say("✓ Fixed")
		} else {
			a.warn("Servers can't be created there until that's fixed.")
		}
	}
	return nil
}

func (a *App) instanceMove() *cobra.Command {
	var to string
	c := &cobra.Command{
		Use: "move <id>", Short: "Move a server's data to another storage location", Args: cobra.ExactArgs(1),
		Long: "Stops the server, copies its data with owners and permissions, checks the copied database, switches the path, and starts it. The old copy is deleted only when you confirm.",
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				inst, err := s.Instance(id)
				if err != nil {
					return err
				}
				if inst.Encrypted {
					if s.VaultKey, err = a.unlockVault(); err != nil {
						return err
					}
					defer s.Forget()
				}
				from := inst.Location
				a.say("Moving %s from %s to %s. It's offline while its data is copied.", id, from, to)
				ok, err := a.term.confirm("Go ahead?")
				if err != nil || !ok {
					return err
				}
				return a.logged("instance move", id, func() error {
					if err := s.Move(cmd.Context(), id, to); err != nil {
						return err
					}
					a.say("✓ %s now runs from %s.", id, to)
					paths, size := s.OldCopy(id, from)
					if len(paths) == 0 {
						return nil
					}
					a.say("The old copy is still at %s (%s).", strings.Join(paths, " and "), gibStr(uint64(size)))
					if !a.term.isTerminal() && !a.Globals.Yes {
						a.say("Delete it once you've checked the server, with: rm -r %s", strings.Join(paths, " "))
						return nil
					}
					ok, err := a.term.confirm("Delete the old copy now?")
					if err != nil || !ok {
						return err
					}
					if err := s.DeleteOldCopy(cmd.Context(), id, from); err != nil {
						return err
					}
					a.say("✓ Old copy deleted")
					return nil
				})
			})
		},
	}
	c.Flags().StringVar(&to, "to", "", "the storage location to move to")
	_ = c.MarkFlagRequired("to")
	return c
}

func (a *App) instanceGrow() *cobra.Command {
	var size int
	c := &cobra.Command{
		Use: "grow <id>", Short: "Make an encrypted server's container bigger", Args: cobra.ExactArgs(1),
		Long: "The server is offline for a moment while its container grows. A full container stops Sharkord saving messages.",
		RunE: func(cmd *cobra.Command, args []string) error {
			if size <= 0 {
				return usageErr("give the new size with --size, in GiB")
			}
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				var err error
				if s.VaultKey, err = a.unlockVault(); err != nil {
					return err
				}
				defer s.Forget()
				return a.logged("instance grow", args[0], func() error {
					if err := s.Grow(cmd.Context(), args[0], size); err != nil {
						return err
					}
					a.say("✓ %s's container is now %d GiB.", args[0], size)
					return nil
				})
			})
		},
	}
	c.Flags().IntVar(&size, "size", 0, "the new container size in GiB")
	return c
}

// unlockServers opens every locked encrypted server; used by vault unlock.
func (a *App) unlockServers(cmd *cobra.Command) error {
	return a.withOps(state.Exclusive, func(s *ops.Sys) error {
		var err error
		if s.VaultKey, err = a.unlockVault(); err != nil {
			return err
		}
		defer s.Forget()
		a.say("The vault is unlocked.")
		var errs []error
		for _, r := range s.UnlockAll(cmd.Context()) {
			a.log.Action("unlock", r.Err, "instance", r.ID)
			if r.Err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", r.ID, r.Err))
				a.say("  ✗ %s: %v", r.ID, r.Err)
				continue
			}
			a.say("  ✓ %s unlocked", r.ID)
		}
		return errors.Join(errs...)
	})
}
