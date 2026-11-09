package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/auth"
	"github.com/GenericD00d/shiver-server-manager/internal/caddy"
	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/validate"
	"github.com/GenericD00d/shiver-server-manager/internal/web"
)

const webUnit = "shiver-server-manager-web.service"

// WebListen is where the web page listens: loopback only, always.
const WebListen = "127.0.0.1:8443"

func (a *App) webCmd() *cobra.Command {
	var listen string
	serve := &cobra.Command{
		Use: "serve", Short: "Run the web page (the service runs this)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.needsLogin() {
				return usageErr("the web page runs as a service; switch it on with: web enable")
			}
			host, _, err := net.SplitHostPort(listen)
			if err != nil || !net.ParseIP(host).IsLoopback() {
				return usageErr("the web page only listens on a loopback address (it's reached through an SSH tunnel or Caddy)")
			}
			st, err := state.Load(paths.StateFile)
			if err != nil {
				return err
			}
			self, err := os.Executable()
			if err != nil {
				return err
			}
			if self, err = filepath.EvalSymlinks(self); err != nil {
				return err
			}
			var hosts []string
			if st.Settings.WebDomain != "" {
				hosts = append(hosts, st.Settings.WebDomain)
			}
			srv := &web.Server{
				Version:  a.Version,
				Sessions: web.NewSessions(),
				Jobs:     web.NewJobs(web.ExecRunner(self)),
				Read:     web.ExecReader(self),
				Login: func(user string, p auth.Prompter) error {
					return (&auth.Login{Auth: auth.PAM{Service: paths.PAMService}, Throttle: &auth.Throttle{Path: paths.LoginsFile}}).Run(user, p)
				},
				LogFile:    paths.LogFile,
				Hosts:      hosts,
				Static:     web.StaticFS(),
				HealthFile: paths.HealthFile,
				RootCert:   caddy.RootCertPath,
			}
			hs := &http.Server{Addr: listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			go func() {
				<-ctx.Done()
				sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = hs.Shutdown(sctx)
			}()
			fmt.Fprintf(a.Err, "%s web page on http://%s\n", paths.AppName, listen)
			if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	serve.Flags().StringVar(&listen, "listen", WebListen, "loopback address and port")

	var domain, tls string
	enable := &cobra.Command{
		Use:   "enable",
		Short: "Switch on the web page",
		Long: "Starts the web page on " + WebListen + ", which only this machine can reach. Open it from your own computer through an SSH tunnel:\n\n" +
			"    ssh -L 8443:localhost:8443 you@server   then   http://localhost:8443\n\n" +
			"With --domain, Caddy also serves it on that domain over HTTPS, so it can be opened from anywhere (after the same login). The domain must point at this server.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("web enable", "", func() error { return a.enableWeb(cmd.Context(), s, domain, state.TLS(tls)) })
			})
		},
	}
	enable.Flags().StringVar(&domain, "domain", "", "also serve it on this domain through Caddy (HTTPS)")
	enable.Flags().StringVar(&tls, "tls", "letsencrypt", "the domain's certificate: letsencrypt or selfsigned")

	disable := &cobra.Command{
		Use: "disable", Short: "Switch off the web page (and its domain)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Exclusive, func(s *ops.Sys) error {
				return a.logged("web disable", "", func() error {
					if err := s.Systemd.DisableNow(cmd.Context(), webUnit); err != nil {
						return err
					}
					if err := s.Caddy.Apply(cmd.Context(), map[string][]byte{adminSitePath(s): nil}, false); err != nil {
						return err
					}
					s.St.Settings.WebDomain, s.St.Settings.WebTLS = "", ""
					a.say("✓ The web page is off.")
					return s.Save()
				})
			})
		},
	}
	status := &cobra.Command{
		Use: "status", Short: "Is the web page on, and where", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.withOps(state.Shared, func(s *ops.Sys) error {
				st, err := s.Systemd.Status(cmd.Context(), webUnit)
				if err != nil {
					return err
				}
				if st.Active != "active" {
					a.say("The web page is off. Switch it on with: web enable")
					return nil
				}
				a.printWebAccess(s)
				return nil
			})
		},
	}
	c := &cobra.Command{Use: "web", Short: "The web page", Args: cobra.NoArgs}
	c.AddCommand(serve, enable, disable, status)
	return c
}

func adminSitePath(s *ops.Sys) string { return filepath.Join(s.Caddy.SitesDir, "_admin.caddy") }

// adminSite proxies the admin domain to the web page.
func adminSite(domain string, tls state.TLS) []byte {
	t := ""
	if tls == state.TLSSelfSigned {
		t = "\ttls {\n\t\tissuer internal {\n\t\t\tlifetime 365d\n\t\t}\n\t}\n"
	}
	return []byte(fmt.Sprintf("# %s (web page). Do not edit.\n%s {\n%s\treverse_proxy %s\n}\n", paths.Marker, domain, t, WebListen))
}

func (a *App) enableWeb(ctx context.Context, s *ops.Sys, domain string, tls state.TLS) error {
	if domain != "" {
		d, err := validate.Domain(domain)
		if err != nil {
			return err
		}
		for id, inst := range s.St.Instances {
			if inst.Domain == d {
				return fmt.Errorf("server %q already uses %s", id, d)
			}
		}
		if tls != state.TLSLetsEncrypt && tls != state.TLSSelfSigned {
			return usageErr("--tls must be letsencrypt or selfsigned")
		}
		if err := s.Caddy.Apply(ctx, map[string][]byte{adminSitePath(s): adminSite(d, tls)}, false); err != nil {
			return err
		}
		s.St.Settings.WebDomain, s.St.Settings.WebTLS = d, tls
		if err := s.Save(); err != nil {
			return err
		}
	}
	if err := s.Systemd.EnableNow(ctx, webUnit); err != nil {
		return err
	}
	// Restart so it picks up the domain (an accepted Host header).
	if err := s.Systemd.Restart(ctx, webUnit); err != nil {
		return err
	}
	a.printWebAccess(s)
	return nil
}

func (a *App) printWebAccess(s *ops.Sys) {
	host, _ := os.Hostname()
	a.say("✓ The web page is on. From your own computer, open an SSH tunnel:\n\n    ssh -L 8443:localhost:8443 <you>@%s\n\nthen browse to http://localhost:8443 and log in with your OS username and password.", host)
	if d := s.St.Settings.WebDomain; d != "" {
		a.say("\nIt's also at https://%s (same login).", d)
	}
}
