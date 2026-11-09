package cli

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

func (a *App) certCmd() *cobra.Command {
	status := &cobra.Command{
		Use: "status [<id>]", Short: "Show certificate status and expiry", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				ids := args
				if len(ids) == 0 {
					ids = s.IDs()
				}
				var infos []ops.CertInfo
				for _, id := range ids {
					ci, err := s.CertInfo(cmd.Context(), id)
					if err != nil {
						return err
					}
					infos = append(infos, ci)
				}
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(infos)
				}
				w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "SERVER\tDOMAIN\tISSUER\tSTATUS")
				for _, ci := range infos {
					status := fmt.Sprintf("valid, %d days left (until %s)", ci.DaysLeft, ci.NotAfter.Format("2006-01-02"))
					if ci.Pending {
						status = "pending: Caddy is still trying"
						if ci.LastError != "" {
							status += "; last error: " + ci.LastError
						}
					}
					if ci.Warning != "" {
						status += "; warning: " + ci.Warning
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ci.Instance, ci.Domain, ci.Issuer, status)
				}
				return w.Flush()
			})
		},
	}
	retry := &cobra.Command{
		Use: "retry <id>", Short: "Recheck DNS and retry getting a certificate now", Args: cobra.ExactArgs(1),
		Long: "Caddy retries a failed certificate by itself for up to 30 days, but the gaps grow to hours. This rechecks DNS, then reloads Caddy so it tries again at once.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("cert retry", args[0], func() error {
					if _, err := s.RetryCert(cmd.Context(), args[0]); err != nil {
						return err
					}
					a.say("✓ Caddy is retrying. Check with: cert status %s", args[0])
					return nil
				})
			})
		},
	}
	certSwitch := stub("switch <id>", "Switch a server's certificate type", cobra.ExactArgs(1))
	certSwitch.Flags().String("tls", "", "letsencrypt or selfsigned")
	_ = certSwitch.MarkFlagRequired("tls")
	certRoot := stub("root", "Show or save the self-signed root certificate", cobra.NoArgs)
	certRoot.Flags().String("out", "", "save the root certificate to this file")

	c := &cobra.Command{Use: "cert", Short: "Certificates", Args: cobra.NoArgs}
	c.AddCommand(status, retry, certSwitch, certRoot)
	return c
}

func (a *App) firewallCmd() *cobra.Command {
	status := &cobra.Command{
		Use: "status", Short: "Show the manager's firewall rules", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				fw := s.Firewall()
				rules, err := fw.List(cmd.Context())
				if err != nil {
					return err
				}
				if a.Globals.JSON {
					if rules == nil {
						rules = []firewall.Rule{}
					}
					return json.NewEncoder(a.Out).Encode(map[string]any{"firewall": fw.Name(), "rules": rules})
				}
				fmt.Fprintf(a.Out, "Firewall: %s\n", fw.Name())
				w := tabwriter.NewWriter(a.Out, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "PORT\tFOR")
				for _, r := range rules {
					fmt.Fprintf(w, "%d/%s\t%s\n", r.Port, r.Proto, r.Instance)
				}
				return w.Flush()
			})
		},
	}
	c := &cobra.Command{Use: "firewall", Short: "Firewall rules", Args: cobra.NoArgs}
	c.AddCommand(status, a.firewallRepairCmd())
	return c
}
