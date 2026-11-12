package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/plugins"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

func (a *App) shiverCmd() *cobra.Command {
	status := &cobra.Command{
		Use: "status", Short: "Show each server's Shiver version next to the latest", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				latest := ""
				rel, err := s.Shiver.Latest(cmd.Context())
				if err == nil {
					latest = rel.PluginVersion()
				} else {
					a.warn("Couldn't look up the latest Shiver: %v", err)
				}
				var out []ops.ShiverStatus
				for _, id := range s.IDs() {
					out = append(out, s.ShiverStatus(cmd.Context(), id, latest))
				}
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(out)
				}
				w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "SERVER\tINSTALLED\tLATEST\tSWITCHED ON")
				for _, st := range out {
					inst := st.Installed
					if inst == "" {
						inst = "-"
					} else if latest != "" && inst != latest {
						inst += " (update available)"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", st.Instance, inst, latest, st.Switches)
				}
				return w.Flush()
			})
		},
	}
	install := func(use, short, action string) *cobra.Command {
		var all bool
		c := &cobra.Command{
			Use: use, Short: short, Args: cobra.MaximumNArgs(1),
			Long: "Downloads the plugin from the latest Shiver release, checks it against that release's SHA256SUMS.txt and its manifest, backs up the server, swaps the plugin in, and restarts. If Sharkord can't load it, the previous copy goes back. Removing Shiver is done in Sharkord's own Settings -> Plugins, which also deletes the data Shiver keeps for each user.",
			RunE: func(cmd *cobra.Command, args []string) error {
				if all == (len(args) == 1) {
					return usageErr("give one server ID, or --all")
				}
				return a.withOps(state.Exclusive, func(s *ops.Sys) error {
					ids := args
					if all {
						for _, id := range s.IDs() {
							if st := s.ShiverStatus(cmd.Context(), id, ""); st.Installed != "" {
								ids = append(ids, id)
							}
						}
					}
					var errs []error
					for _, id := range ids {
						err := a.logged(action, id, func() error {
							note, err := s.InstallShiver(cmd.Context(), id)
							if err != nil {
								return err
							}
							a.say("✓ Shiver is running on %s.", id)
							if note != "" {
								a.say("! %s", note)
							}
							return nil
						})
						if err != nil {
							a.say("  ✗ %s: %v", id, err)
							errs = append(errs, fmt.Errorf("%s: %w", id, err))
						}
					}
					return errors.Join(errs...)
				})
			},
		}
		if action == "shiver update" {
			c.Flags().BoolVar(&all, "all", false, "every server that has Shiver, one at a time")
		}
		return c
	}
	c := &cobra.Command{Use: "shiver", Short: "The Shiver plugin", Args: cobra.NoArgs}
	c.AddCommand(status,
		install("install <id>", "Install Shiver on a server", "shiver install"),
		install("update <id>|--all", "Update Shiver on a server, or everywhere", "shiver update"))
	return c
}

// internalEnablePluginsCmd is run by the manager itself, as an instance's
// user, to switch plugins on in its stopped database. It refuses root.
func (a *App) internalEnablePluginsCmd() *cobra.Command {
	return &cobra.Command{
		Use: "internal-enable-plugins <db> [<plugin-id>]", Hidden: true, Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := refuseRoot(); err != nil {
				return err
			}
			id := plugins.ID
			if len(args) == 2 {
				id = args[1]
			}
			return plugins.EnableInDB(args[0], id)
		},
	}
}

// internalSetTokenCmd is run by the manager itself, as an instance's user,
// to put a new token's hash (read from stdin) in its stopped database. It
// refuses root.
func (a *App) internalSetTokenCmd() *cobra.Command {
	return &cobra.Command{
		Use: "internal-set-token <db>", Hidden: true, Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := refuseRoot(); err != nil {
				return err
			}
			b, err := io.ReadAll(io.LimitReader(a.In, 256))
			if err != nil {
				return err
			}
			return sharkord.SetTokenHash(args[0], strings.TrimSpace(string(b)))
		},
	}
}
