package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/plugins"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

// PluginList is what `plugin list --json` prints per server.
type PluginList struct {
	Instance string             `json:"instance"`
	Plugins  []plugins.Manifest `json:"plugins"`
}

func (a *App) pluginCmd() *cobra.Command {
	list := &cobra.Command{
		Use: "list [<id>]", Short: "List the plugins installed on each server", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				ids := args
				if len(ids) == 0 {
					ids = s.IDs()
				}
				out := []PluginList{}
				for _, id := range ids {
					ps, err := s.Plugins(id)
					if err != nil {
						return err
					}
					if ps == nil {
						ps = []plugins.Manifest{}
					}
					out = append(out, PluginList{Instance: id, Plugins: ps})
				}
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(out)
				}
				w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "SERVER\tPLUGIN\tVERSION\tNAME")
				for _, l := range out {
					for _, m := range l.Plugins {
						fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", l.Instance, m.ID, m.Version, m.Name)
					}
				}
				return w.Flush()
			})
		},
	}
	install := &cobra.Command{
		Use:   "install <id> <file>",
		Short: "Install or update a plugin on a server from a .zip or .tar(.gz/.zst) file",
		Long: "The archive must hold a Sharkord plugin: manifest.json, server/index.js and client/index.js, at the top or in one folder. " +
			"Its manifest must speak the plugin SDK of the server's Sharkord. The server is backed up, the plugin is swapped in while it's stopped, " +
			"and plugins (and this plugin) are switched on the first time. If Sharkord can't load it, the previous copy goes back. " +
			"Only install plugins you trust: they run inside Sharkord with access to its data.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			file, err := filepath.Abs(args[1])
			if err != nil {
				return err
			}
			// A file uploaded through the web page goes once it's been used.
			if filepath.Dir(file) == paths.UploadsDir {
				defer os.Remove(file)
			}
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("plugin install", id, func() error {
					m, note, err := s.InstallPluginFile(cmd.Context(), id, file)
					if err != nil {
						return err
					}
					name := m.Name
					if name == "" {
						name = m.ID
					}
					a.say("✓ %s %s is running on %s.", name, m.Version, id)
					if note != "" {
						a.say("! %s", strings.TrimSpace(note))
					}
					return nil
				})
			})
		},
	}
	catalog := &cobra.Command{
		Use:   "catalog",
		Short: "List the Shiver plugins on offer, with their latest versions and what each server has",
		Long:  "The list is plugins.json in the manager's own repository; each plugin comes from its own GitHub releases.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				cat, err := s.PluginCatalog(cmd.Context())
				if err != nil {
					return err
				}
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(cat)
				}
				w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "PLUGIN\tLATEST\tSERVER\tINSTALLED")
				for _, p := range cat {
					latest := p.Latest
					if p.Error != "" {
						latest = "unknown: " + p.Error
					}
					if len(p.Servers) == 0 {
						fmt.Fprintf(w, "%s\t%s\t\t\n", p.ID, latest)
					}
					for i, sv := range p.Servers {
						installed := sv.Installed
						switch {
						case installed == "":
							installed = "-"
						case sv.Update:
							installed += " (update available)"
						}
						if i > 0 {
							fmt.Fprintf(w, "\t\t%s\t%s\n", sv.Instance, installed)
						} else {
							fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", p.ID, latest, sv.Instance, installed)
						}
					}
				}
				return w.Flush()
			})
		},
	}
	add := &cobra.Command{
		Use:   "add <id> <plugin>",
		Short: "Install or update a Shiver plugin (see plugin catalog) on a server from its latest release",
		Long: "Downloads the plugin's latest release and checks its checksum, then installs it like a plugin from a file: " +
			"the server is backed up, the plugin is swapped in while it's stopped, and the previous copy goes back if Sharkord can't load it.",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, pluginID := args[0], args[1]
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("plugin add "+pluginID, id, func() error {
					m, note, err := s.InstallFromCatalog(cmd.Context(), id, pluginID)
					if err != nil {
						return err
					}
					name := m.Name
					if name == "" {
						name = m.ID
					}
					a.say("✓ %s %s is running on %s.", name, m.Version, id)
					if note != "" {
						a.say("! %s", strings.TrimSpace(note))
					}
					return nil
				})
			})
		},
	}
	c := &cobra.Command{Use: "plugin", Short: "Sharkord plugins: the Shiver plugins on offer, install from a file, list", Args: cobra.NoArgs,
		Long: "Remove a plugin in Sharkord's own Settings -> Plugins."}
	c.AddCommand(list, install, catalog, add)
	return c
}
