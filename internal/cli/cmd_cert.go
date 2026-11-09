package cli

import (
	"encoding/json"
	"fmt"
	"os"
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
	var tls string
	var testMode bool
	certSwitch := &cobra.Command{
		Use: "switch <id>", Short: "Switch a server's certificate type (or Let's Encrypt test mode)", Args: cobra.ExactArgs(1),
		Long: "Writes the new site file, waits for the new certificate, then deletes the old type's certificate. Members of a self-signed server need its root certificate installed; see 'cert root'.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				inst, err := s.Instance(args[0])
				if err != nil {
					return err
				}
				a.say("Switching %s (%s) from %s to %s.", args[0], inst.Domain, describeTLS(inst.TLS, inst.TestMode), describeTLS(state.TLS(tls), testMode))
				if state.TLS(tls) == state.TLSSelfSigned {
					a.say("Members will see a certificate warning until they install the root certificate (see 'cert root').")
				}
				ok, err := a.term.confirm("Switch?")
				if err != nil || !ok {
					return err
				}
				return a.logged("cert switch", args[0], func() error {
					if err := s.SwitchTLS(cmd.Context(), args[0], state.TLS(tls), testMode); err != nil {
						return err
					}
					a.say("✓ %s now uses %s.", args[0], describeTLS(state.TLS(tls), testMode))
					return nil
				})
			})
		},
	}
	certSwitch.Flags().StringVar(&tls, "tls", "", "letsencrypt or selfsigned")
	certSwitch.Flags().BoolVar(&testMode, "test-mode", false, "use Let's Encrypt's staging server")
	_ = certSwitch.MarkFlagRequired("tls")

	var out string
	certRoot := &cobra.Command{
		Use: "root", Short: "Show or save the self-signed root certificate members install", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				r, err := s.RootInfo()
				if err != nil {
					return err
				}
				if out != "" {
					if err := os.WriteFile(out, r.PEM, 0o644); err != nil {
						return err
					}
					_ = os.Chmod(out, 0o644)
					a.log.Action("cert root", nil, "out", out)
				}
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(r)
				}
				a.say("SHA-256 fingerprint:\n  %s\n", r.Fingerprint)
				a.say("Expires: %s", r.NotAfter.Format("2006-01-02"))
				if r.Warning != "" {
					a.say("! %s", r.Warning)
				}
				if len(r.URLs) > 0 {
					a.say("\nMembers download it from:")
					for _, u := range r.URLs {
						a.say("  %s", u)
					}
				}
				if out != "" {
					a.say("\nSaved to %s", out)
				}
				a.say("\nThe download isn't encrypted, so send members the fingerprint another way (chat, email)\nand ask them to compare it before trusting the file. Install steps: see Help, \"Install the root certificate\".")
				return nil
			})
		},
	}
	certRoot.Flags().StringVar(&out, "out", "", "also save the root certificate to this file")

	c := &cobra.Command{Use: "cert", Short: "Certificates", Args: cobra.NoArgs}
	c.AddCommand(status, retry, certSwitch, certRoot)
	return c
}

func describeTLS(t state.TLS, testMode bool) string {
	switch {
	case t == state.TLSSelfSigned:
		return "a self-signed certificate"
	case testMode:
		return "Let's Encrypt's staging server (test mode)"
	}
	return "Let's Encrypt"
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
	c.AddCommand(status, a.firewallRepairCmd(), a.upnpCmd())
	return c
}
