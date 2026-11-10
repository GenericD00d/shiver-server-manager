package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/netinfo"
	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

func (a *App) healthCmd() *cobra.Command {
	var unattended bool
	c := &cobra.Command{
		Use:   "health",
		Short: "Run the health check",
		Long:  "Checks each server (running, answering, unlocked, room in its container), certificates and the self-signed root, the public address, firewall rules and site files, Caddy, free space and drives, and new Sharkord and Shiver versions. Problems are sent by email or webhook when they appear or clear (see 'health alerts').",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if unattended && a.needsLogin() {
				return usageErr("--unattended is for the timer, which runs as root")
			}
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				rep, appeared, cleared := s.Health(cmd.Context(), ops.HealthOpts{Unattended: unattended, Online: true})
				if err := s.SaveReport(rep); err != nil {
					return err
				}
				if len(appeared) > 0 || len(cleared) > 0 {
					if alerts := s.LoadAlerts(); alerts.Configured() {
						host, _ := os.Hostname()
						subject := fmt.Sprintf("%s: %d new, %d fixed", paths.AppName, len(appeared), len(cleared))
						if err := alerts.Send(cmd.Context(), subject, ops.AlertText(host, appeared, cleared)); err != nil {
							a.warn("Couldn't send the alert: %v", err)
						}
					}
				}
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(rep)
				}
				if len(rep.Notices) == 0 {
					a.say("✓ Everything is healthy.")
					return nil
				}
				for _, n := range rep.Notices {
					mark := map[string]string{"error": "✗", "warning": "!", "info": "·"}[n.Level]
					a.say("%s %s", mark, n.Message)
				}
				return nil
			})
		},
	}
	c.Flags().BoolVar(&unattended, "unattended", false, "run from the timer: no login, no prompts, follow public IP changes")

	var webhook, emailTo, smtpServer, smtpUser, from string
	var passStdin, off, test bool
	alerts := &cobra.Command{
		Use:   "alerts",
		Short: "Send health problems by email or webhook",
		Long:  "With no flags, shows the settings. A webhook gets a JSON POST with \"text\" and \"content\" fields (Slack, Mattermost, Discord). The settings live in a root-only file, since the health timer runs without the vault.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				al := s.LoadAlerts()
				changed := false
				if off {
					al, changed = ops.Alerts{}, true
				}
				if webhook != "" {
					al.Webhook, changed = webhook, true
				}
				if emailTo != "" {
					al.Email.To, al.Email.Server, al.Email.User, al.Email.From, changed = emailTo, smtpServer, smtpUser, from, true
					if smtpServer == "" {
						return usageErr("--email-to needs --smtp host:port")
					}
					if passStdin {
						p, err := a.term.hidden("SMTP password: ", true)
						if err != nil {
							return err
						}
						al.Email.Password = p
					}
				}
				if changed {
					if err := s.SaveAlerts(al); err != nil {
						return err
					}
					a.log.Action("health alerts", nil)
				}
				if test {
					host, _ := os.Hostname()
					return al.Send(cmd.Context(), paths.AppName+": test alert", fmt.Sprintf("A test alert from %s on %s.", paths.AppName, host))
				}
				if a.Globals.JSON {
					return json.NewEncoder(a.Out).Encode(map[string]any{
						"configured": al.Configured(), "webhook": al.Webhook,
						"emailTo": al.Email.To, "smtp": al.Email.Server, "smtpUser": al.Email.User, "emailFrom": al.Email.From,
					})
				}
				switch {
				case !al.Configured():
					a.say("Alerts are off.")
				default:
					if al.Webhook != "" {
						a.say("Webhook: %s", al.Webhook)
					}
					if al.Email.To != "" {
						a.say("Email: to %s via %s", al.Email.To, al.Email.Server)
					}
				}
				return nil
			})
		},
	}
	f := alerts.Flags()
	f.StringVar(&webhook, "webhook", "", "webhook URL")
	f.StringVar(&emailTo, "email-to", "", "email address(es), comma-separated")
	f.StringVar(&smtpServer, "smtp", "", "mail server, host:port")
	f.StringVar(&smtpUser, "smtp-user", "", "mail server login")
	f.StringVar(&from, "email-from", "", "sender address (default: the login)")
	f.BoolVar(&passStdin, "smtp-password-stdin", false, "read the mail server password from stdin")
	f.BoolVar(&off, "off", false, "turn alerts off")
	f.BoolVar(&test, "test", false, "send a test alert")
	c.AddCommand(alerts)
	return c
}

func (a *App) upnpCmd() *cobra.Command {
	c := &cobra.Command{
		Use:       "upnp on|off|status",
		Short:     "Forward ports on a home router with UPnP",
		Long:      "Many routers ship with UPnP off, and some admins prefer to keep it that way; the fallback is forwarding the ports by hand (see Help, \"Forward ports on a home router\").",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"on", "off", "status"},
		RunE: func(cmd *cobra.Command, args []string) error {
			mode := state.Shared
			if args[0] != "status" {
				mode = state.Exclusive
			}
			return a.withOps(mode, func(s *ops.Sys) error {
				ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
				defer cancel()
				router, err := s.FindRouter(ctx)
				if err != nil {
					return err
				}
				wan, _ := router.ExternalIP(ctx)
				problem := netinfo.NATProblem(wan, s.St.Settings.PublicIPv4)
				switch args[0] {
				case "status":
					a.say("Router found; its internet address is %s. Forwarding is %s.", wan, map[bool]string{true: "on", false: "off"}[s.St.Settings.UPnP])
				case "on":
					if problem != "" {
						return errors.New(problem)
					}
					s.St.Settings.UPnP = true
					if err := s.Save(); err != nil {
						return err
					}
					err := s.UPnPMapAll(cmd.Context())
					a.log.Action("upnp on", err)
					if err != nil && allPortsTaken(err) {
						// Every port is forwarded by rules set by hand already:
						// UPnP has nothing to add, and the health check would
						// only keep reporting the same refusals.
						s.St.Settings.UPnP = false
						if serr := s.Save(); serr != nil {
							return serr
						}
						return fmt.Errorf("the router already forwards every port with rules set by hand, so UPnP isn't needed and stays off:\n%v\nIf outside traffic still doesn't arrive, check those rules point at this machine (%s)", err, lanHint(router))
					}
					if err != nil {
						return err
					}
					a.say("✓ The router forwards TCP 80 and 443, UDP 443 and each server's voice port to this machine. The health check renews it.")
				case "off":
					var errs []error
					for _, p := range []struct {
						port  int
						proto string
					}{{80, "TCP"}, {443, "TCP"}, {443, "UDP"}} {
						errs = append(errs, router.Unmap(cmd.Context(), p.port, p.proto))
					}
					for _, id := range s.IDs() {
						errs = append(errs, s.UPnPUnmap(cmd.Context(), s.St.Instances[id].WebRTCPort))
					}
					s.St.Settings.UPnP = false
					errs = append(errs, s.Save())
					a.log.Action("upnp off", errors.Join(errs...))
					a.say("✓ Port forwarding removed from the router.")
				default:
					return usageErr("say on, off or status")
				}
				if problem != "" {
					a.warn("%s", problem)
				}
				return nil
			})
		},
	}
	return c
}

// describe a desktop's sleep settings is left to Help; setup offers the
// inhibitor service.
const inhibitUnit = `# Managed by shiver-server-manager. Do not edit.
[Unit]
Description=Keep this computer awake for its Sharkord servers

[Service]
ExecStart=/usr/bin/systemd-inhibit --what=sleep:idle --mode=block --who=shiver-server-manager --why="Sharkord servers are running" sleep infinity
Restart=always

[Install]
WantedBy=multi-user.target
`

func runCmd(name string, args ...string) run.Cmd { return run.Cmd{Name: name, Args: args} }

func isDesktop(ctx context.Context, a *App) bool {
	res, err := a.runner.Run(ctx, runCmd("systemctl", "get-default"))
	return err == nil && strings.TrimSpace(string(res.Stdout)) == "graphical.target"
}

// allPortsTaken: every error in a joined error is netinfo.ErrPortTaken.
func allPortsTaken(err error) bool {
	var list []error
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		list = j.Unwrap()
	} else {
		list = []error{err}
	}
	for _, e := range list {
		if !errors.Is(e, netinfo.ErrPortTaken) {
			return false
		}
	}
	return len(list) > 0
}

func lanHint(router ops.PortMapper) string {
	if u, ok := router.(interface{ LANAddress() (string, error) }); ok {
		if ip, err := u.LANAddress(); err == nil {
			return ip
		}
	}
	return "its LAN address"
}
