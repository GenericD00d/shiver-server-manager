package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
)

func (a *App) instanceCmd() *cobra.Command {
	c := &cobra.Command{Use: "instance", Short: "Servers: list, create, start, stop, settings, update, move, delete", Args: cobra.NoArgs}
	c.AddCommand(a.instanceList(), a.instanceShow(), a.instanceCreate(),
		a.instanceAction("start", "Start a server", (*ops.Sys).Start),
		a.instanceAction("stop", "Stop a server", (*ops.Sys).Stop),
		a.instanceAction("restart", "Restart a server", (*ops.Sys).Restart),
		a.instanceLogs(), a.instanceSet())
	return c
}

func (a *App) instanceList() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List servers", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				infos := []ops.Info{}
				for _, id := range s.IDs() {
					info, err := s.Info(cmd.Context(), id)
					if err != nil {
						return err
					}
					infos = append(infos, info)
				}
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(infos)
				}
				if len(infos) == 0 {
					fmt.Fprintln(a.Out, "No servers yet. Create one with: instance create --id <id> --domain <domain>")
					return nil
				}
				w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "SERVER\tSTATUS\tRUNNING\tDOMAIN\tCERTIFICATE\tLOCATION")
				for _, i := range infos {
					status := string(i.Status)
					if i.Operation != nil {
						status += " (unfinished " + i.Operation.Name + ")"
					}
					running := i.Active
					if i.Locked {
						running = "locked (run 'vault unlock')"
					} else if i.Active == "active" && !i.Healthy {
						running = "not answering"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", i.ID, status, running, i.Domain, i.TLS, i.Location)
				}
				return w.Flush()
			})
		},
	}
}

func (a *App) instanceShow() *cobra.Command {
	return &cobra.Command{
		Use: "show <id>", Short: "Show one server", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				info, err := s.Info(cmd.Context(), args[0])
				if err != nil {
					return err
				}
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(info)
				}
				w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
				row := func(k string, v any) { fmt.Fprintf(w, "%s\t%v\n", k, v) }
				row("Server", info.ID)
				row("Status", info.Status)
				row("Running", info.Active)
				row("Answering", info.Healthy)
				row("Domain", info.Domain)
				row("Certificate", info.TLS)
				if info.TestMode {
					row("Test mode", "on (Let's Encrypt staging)")
				}
				row("Sharkord", info.SharkordVersion)
				row("Location", info.Location)
				row("Data folder", info.DataPath)
				row("Encrypted", info.Encrypted)
				row("Web port", fmt.Sprintf("%d (127.0.0.1 only)", info.HTTPPort))
				row("Voice port", fmt.Sprintf("%d (UDP and TCP)", info.WebRTCPort))
				row("Secret token saved", info.TokenSaved)
				for k, v := range info.Settings {
					row(k, v)
				}
				if op := info.Operation; op != nil {
					row("Unfinished", fmt.Sprintf("%s, last step %q", op.Name, op.LastStep))
					if op.Error != "" {
						row("Failed at", fmt.Sprintf("%s: %s", op.FailedStep, op.Error))
					}
				}
				return w.Flush()
			})
		},
	}
}

func (a *App) instanceCreate() *cobra.Command {
	var r ops.CreateRequest
	var tls string
	var noEncrypt bool
	c := &cobra.Command{
		Use: "create", Short: "Create a server", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			r.TLS = state.TLS(tls)
			r.Encrypted = !noEncrypt
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("instance create", r.ID, func() error {
					if err := s.CheckCreate(cmd.Context(), &r); err != nil {
						return err
					}
					a.say("Creating server %s for %s (Sharkord %s)", r.ID, r.Domain, r.Version)
					if err := s.Create(cmd.Context(), r); err != nil {
						return err
					}
					a.say("Server %s is running.", r.ID)
					return nil
				})
			})
		},
	}
	f := c.Flags()
	f.StringVar(&r.ID, "id", "", "server ID: lowercase letters, digits and hyphens, starting with a letter, up to 20 characters")
	f.StringVar(&r.Domain, "domain", "", "the domain members will use")
	f.StringVar(&tls, "tls", "letsencrypt", "certificate type: letsencrypt or selfsigned")
	f.BoolVar(&r.TestMode, "test-mode", false, "use Let's Encrypt's staging server")
	f.StringVar(&r.Location, "location", "", "storage location (default: the default location)")
	f.BoolVar(&noEncrypt, "no-encrypt", false, "don't put the data folder in an encrypted container")
	f.BoolVar(&r.SkipDNSCheck, "skip-dns-check", false, "don't check the domain points here (it points at a proxy in front of the server)")
	f.StringVar(&r.Version, "version", "", "Sharkord version (default: the newest release)")
	_ = c.MarkFlagRequired("id")
	_ = c.MarkFlagRequired("domain")
	return c
}

func (a *App) instanceAction(name, short string, fn func(*ops.Sys, context.Context, string) error) *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use: name + " <id>|--all", Short: short, Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) {
				return usageErr("give one server ID, or --all")
			}
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				ids := args
				if all {
					ids = s.IDs()
				}
				var errs []error
				for _, id := range ids {
					err := a.logged("instance "+name, id, func() error { return fn(s, cmd.Context(), id) })
					if err != nil {
						errs = append(errs, fmt.Errorf("%s: %w", id, err))
						continue
					}
					a.say("✓ %s %s", name, id)
				}
				return errors.Join(errs...)
			})
		},
	}
	c.Flags().BoolVar(&all, "all", false, "every server")
	return c
}

func (a *App) instanceLogs() *cobra.Command {
	var follow bool
	var lines int
	c := &cobra.Command{
		Use: "logs <id>", Short: "Show a server's logs", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]
			if follow {
				// Streams until interrupted, so it runs directly rather than
				// through the runner. Never a pager.
				if err := a.withOps(state.Shared, func(s *ops.Sys) error { _, err := s.Instance(id); return err }); err != nil {
					return err
				}
				j := exec.CommandContext(cmd.Context(), "/usr/bin/journalctl", "-u", systemd.Unit(id), "-n", fmt.Sprint(lines), "-f", "--no-pager", "-o", "short-iso")
				j.Stdout, j.Stderr = a.Out, a.Err
				return j.Run()
			}
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				inst, err := s.Instance(id)
				if err != nil {
					return err
				}
				out, err := s.Logs(cmd.Context(), id, lines)
				fmt.Fprint(a.Out, out)
				if b, rerr := os.ReadFile(inst.DataPath + "/logs/error.log"); rerr == nil && len(b) > 0 {
					fmt.Fprintf(a.Out, "\n--- %s/logs/error.log (last lines) ---\n%s", inst.DataPath, lastN(string(b), lines))
				}
				return err
			})
		},
	}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "keep showing new lines")
	c.Flags().IntVarP(&lines, "lines", "n", 100, "how many lines")
	return c
}

func lastN(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n") + "\n"
}

func (a *App) instanceSet() *cobra.Command {
	return &cobra.Command{
		Use:   "set <id> KEY=VALUE...",
		Short: "Change a server's settings, then restart it (KEY= goes back to the default)",
		Long:  "Change a server's Sharkord settings, then restart it. KEY may leave out the SHARKORD_ prefix.\n\n" + settingsHelp(),
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("instance set", args[0], func() error {
					keys := make([]string, 0, len(args)-1)
					for _, p := range args[1:] {
						k, _, _ := strings.Cut(p, "=")
						keys = append(keys, k)
					}
					a.log.Line("settings changed: " + strings.Join(keys, ", "))
					if err := s.Set(cmd.Context(), args[0], args[1:]); err != nil {
						return err
					}
					a.say("✓ Settings saved and %s restarted.", args[0])
					return nil
				})
			})
		},
	}
}
