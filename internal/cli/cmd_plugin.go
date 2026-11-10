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
	c := &cobra.Command{Use: "plugin", Short: "Sharkord plugins: list, install from a file", Args: cobra.NoArgs,
		Long: "Remove a plugin in Sharkord's own Settings -> Plugins. For Shiver, use the shiver commands, which fetch it from its releases."}
	c.AddCommand(list, install)
	return c
}
