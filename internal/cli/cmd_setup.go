package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/GenericD00d/shiver-server-manager/internal/caddy"
	"github.com/GenericD00d/shiver-server-manager/internal/distro"
	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/netinfo"
	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// setupDirs are the manager's folders and their modes.
var setupDirs = []struct {
	path string
	mode os.FileMode
}{
	// 711: instance users must pass through to their version link in bin/
	// (the unit's ExecStart). Nothing in here is readable by them.
	{paths.StateDir, 0o711},
	{paths.VaultDir, 0o700},
	{paths.VersionLinkDir, 0o755},
	{filepath.Join(paths.StateDir, "cache"), 0o700},
	{paths.ConfigDir, 0o700},
	{paths.InstanceEnvDir, 0o700},
	{paths.DefaultDataLocation, 0o711},
	{paths.DefaultBackupLocation, 0o700},
}

type setupFlags struct {
	email        string
	acmeCA       string
	acmeCARoot   string
	noFirewall   bool
	publicIP     string
	containerGiB int
	dataLocation string
}

func (a *App) setupCmd() *cobra.Command {
	var f setupFlags
	c := &cobra.Command{
		Use:   "setup",
		Short: "First-time setup: check the system, install Caddy, set up the firewall, admins and the vault",
		Long:  "First-time setup checks the system and installs what the manager needs. It's safe to run again: each step leaves alone what's already in place.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			admins := osuser.Admins(a.runner)
			members, err := admins.Members(cmd.Context())
			if err != nil {
				return err
			}
			// Once there are admins, only an admin may rerun setup. Before
			// that, whoever has root through sudo is setting the server up.
			if len(members) > 0 {
				if err := a.login(); err != nil {
					return err
				}
			} else {
				a.log.User = a.Env.SudoUser
			}
			lk, err := a.lock(state.Exclusive)
			if err != nil {
				return err
			}
			defer lk.Release()
			err = a.runSetup(cmd.Context(), admins, members, f)
			a.log.Action("setup", err)
			return err
		},
	}
	c.Flags().StringVar(&f.email, "email", "", "email address for Let's Encrypt expiry notices (optional)")
	c.Flags().StringVar(&f.acmeCA, "acme-ca", "", "use this ACME server instead of Let's Encrypt (testing)")
	c.Flags().StringVar(&f.acmeCARoot, "acme-ca-root", "", "root certificate of the --acme-ca server (testing)")
	c.Flags().StringVar(&f.publicIP, "public-ip", "", "use this public IPv4 address instead of looking it up (\"auto\" to look it up again)")
	c.Flags().StringVar(&f.dataLocation, "data-location", "", "default folder for new and imported servers' data (default /var/lib/sharkord)")
	c.Flags().IntVar(&f.containerGiB, "container-size", 0, "size in GiB of each new encrypted container (default 10)")
	c.Flags().BoolVar(&f.noFirewall, "no-firewall", false, "don't switch on ufw if no firewall is active")
	_ = c.Flags().MarkHidden("acme-ca")
	_ = c.Flags().MarkHidden("acme-ca-root")
	return c
}

func (a *App) ok(format string, args ...any)   { a.say("✓ "+format, args...) }
func (a *App) warn(format string, args ...any) { a.say("! "+format, args...) }

func (a *App) runSetup(ctx context.Context, admins *osuser.Group, members []string, f setupFlags) error {
	// The system.
	osInfo, err := ops.OS()
	if err != nil {
		return err
	}
	if err := osInfo.Check(); err != nil {
		return err
	}
	a.ok("%s", osInfo.PrettyName)

	for _, d := range setupDirs {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return err
		}
		if err := os.Chmod(d.path, d.mode); err != nil {
			return err
		}
	}
	st, err := state.Load(paths.StateFile)
	if err != nil {
		return err
	}
	s := ops.New(a.runner, st, a.log, paths.Cmd+"/"+a.Version)
	s.Progress = a.printEvent
	if f.email != "" {
		st.Settings.Email = f.email
	}
	if f.containerGiB > 0 {
		st.Settings.ContainerSizeGiB = f.containerGiB
	}
	if f.acmeCA != "" {
		st.Settings.ACMECA = f.acmeCA
		if f.acmeCARoot != "" {
			b, err := os.ReadFile(f.acmeCARoot)
			if err != nil {
				return err
			}
			dst := filepath.Join(paths.CaddyGlobalDir, "acme-ca-root.pem")
			if err := fsutil.MkdirAllMode(paths.CaddyGlobalDir, 0o755); err != nil {
				return err
			}
			if err := fsutil.WriteFileAtomic(dst, b, 0o644, nil); err != nil {
				return err
			}
			st.Settings.ACMECARoot = dst
		}
	}
	if err := s.Save(); err != nil {
		return err
	}
	a.ok("Folders and records")

	if err := admins.Ensure(ctx); err != nil {
		return err
	}
	if a.needsLogin() {
		if err := admins.Add(ctx, a.Env.SudoUser); err != nil {
			return err
		}
		if !slices.Contains(members, a.Env.SudoUser) {
			a.log.Action("admins add", nil, "admin", a.Env.SudoUser)
		}
		a.ok("%s is an admin (group %s)", a.Env.SudoUser, paths.AdminGroup)
	} else if len(members) == 0 {
		a.warn("No admins yet. Add one with: %s admins add <user>", paths.Cmd)
	}

	if err := a.setupCaddy(ctx, s, osInfo); err != nil {
		return err
	}
	if err := a.setupFirewall(ctx, s, osInfo, f.noFirewall); err != nil {
		return err
	}
	if !haveProgram("cryptsetup") {
		a.say("  … installing cryptsetup, for encrypted data folders")
		if err := s.Installer(osInfo).Install(ctx, "cryptsetup"); err != nil {
			return err
		}
	}
	a.ok("Encrypted data folders: new servers get a %d GiB container", st.Settings.ContainerSizeGiB)
	if err := s.Save(); err != nil {
		return err
	}

	if ok, _ := s.ClockSynced(ctx); ok {
		a.ok("Clock synchronised")
	} else {
		a.warn("The clock isn't synchronised (timedatectl). Certificates fail when the time is wrong; turn on NTP with: timedatectl set-ntp true")
	}

	switch f.publicIP {
	case "":
	case "auto":
		st.Settings.FixedPublicIPv4 = false
	default:
		ip := net.ParseIP(f.publicIP).To4()
		if ip == nil {
			return usageErr("--public-ip %q isn't an IPv4 address", f.publicIP)
		}
		st.Settings.FixedPublicIPv4 = true
		if err := s.ChangePublicIP(ctx, ip.String()); err != nil {
			return err
		}
	}
	if st.Settings.FixedPublicIPv4 {
		a.ok("Public IPv4 address %s (set by hand)", st.Settings.PublicIPv4)
	} else if ip, err := s.PublicIPv4(ctx); err != nil {
		a.warn("Couldn't find the public IPv4 address: %v", err)
	} else {
		if err := s.ChangePublicIP(ctx, ip); err != nil {
			return err
		}
		if slices.Contains(netinfo.LocalIPv4(), ip) {
			a.ok("Public IPv4 address %s, on this machine (like a VPS)", ip)
		} else {
			a.ok("Public IPv4 address %s, behind a router or cloud NAT: the ports below must be forwarded or opened there", ip)
		}
	}

	if !slices.Contains(netinfo.LocalIPv4(), st.Settings.PublicIPv4) {
		// Behind a router: look for it, to spot carrier-grade NAT.
		rctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		if router, err := s.FindRouter(rctx); err == nil {
			wan, _ := router.ExternalIP(rctx)
			if problem := netinfo.NATProblem(wan, st.Settings.PublicIPv4); problem != "" {
				a.warn("%s", problem)
			} else {
				a.ok("Home router found; forward the ports there, or let the manager do it with: firewall upnp on")
			}
		}
		cancel()
	}

	for name, err := range ops.Outbound(ctx, osInfo) {
		if err != nil {
			a.warn("Can't reach %s: %v", name, err)
		}
	}
	if free, err := fsutil.FreeBytes(paths.DefaultDataLocation); err == nil {
		const gib = 1 << 30
		if free < 5*gib {
			a.warn("Only %.1f GiB free for server data at %s; each server needs at least 2 GiB, plus room for uploads", float64(free)/gib, paths.DefaultDataLocation)
		} else {
			a.ok("%.0f GiB free for server data", float64(free)/gib)
		}
	}

	if osInfo.Family() == distro.Debian {
		if ops.AutoUpdatesOn() {
			if err := s.AddCaddyToAutoUpdates(ctx); err != nil {
				a.warn("Couldn't add Caddy to automatic updates: %v", err)
			} else {
				a.ok("Automatic security updates include Caddy")
			}
		} else {
			a.warn("Automatic security updates (unattended-upgrades) are off. Turn them on with: dpkg-reconfigure -plow unattended-upgrades")
		}
	}

	if isDesktop(ctx, a) {
		const unit = "/etc/systemd/system/shiver-server-manager-inhibit.service"
		if _, err := os.Stat(unit); err != nil {
			ok, err := a.term.confirm("This looks like a desktop computer. A sleeping computer takes every community offline. Keep it awake while the manager is installed?")
			if err != nil {
				return err
			}
			if ok {
				if err := fsutil.WriteFileAtomic(unit, []byte(inhibitUnit), 0o644, nil); err != nil {
					return err
				}
				if _, err := a.runner.Run(ctx, runCmd("systemctl", "daemon-reload")); err != nil {
					return err
				}
				if _, err := a.runner.Run(ctx, runCmd("systemctl", "enable", "--now", "shiver-server-manager-inhibit.service")); err != nil {
					return err
				}
				a.ok("Sleep is blocked while the manager is installed (check your desktop's power settings respect it)")
			} else {
				a.warn("Make sure this computer doesn't go to sleep by itself, in your desktop's power settings.")
			}
		}
	}
	for _, t := range []string{"shiver-server-manager-health.timer", "shiver-server-manager-backup.timer"} {
		if _, err := os.Stat("/usr/lib/systemd/system/" + t); err == nil {
			if _, err := a.runner.Run(ctx, runCmd("systemctl", "enable", "--now", t)); err != nil {
				return err
			}
		}
	}

	if f.dataLocation != "" {
		p := filepath.Clean(f.dataLocation)
		known := false
		for _, l := range st.Settings.Locations {
			known = known || l.Path == p
		}
		if !known {
			if _, err := s.AddLocation(ctx, p); err != nil {
				return err
			}
		}
		if err := s.SetDefaultLocation(p); err != nil {
			return err
		}
	}
	a.ok("New and imported servers' data goes to %s", st.DefaultLocation())

	v := a.vault()
	if v.Exists() {
		a.ok("Vault (already set up)")
	} else {
		if err := a.createVault(v); err != nil {
			return err
		}
		a.ok("Vault")
	}
	a.say("\nSetup is done. Members reach servers on TCP 80 and 443, UDP 443, and each server's voice port (UDP and TCP, %d-%d).", st.Settings.WebRTCPorts[0], st.Settings.WebRTCPorts[1])
	return nil
}

// haveProgram reports whether a program is in one of the system folders.
func haveProgram(name string) bool {
	for _, d := range run.SearchDirs {
		if fi, err := os.Stat(filepath.Join(d, name)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

func (a *App) setupCaddy(ctx context.Context, s *ops.Sys, osInfo distro.OS) error {
	inst := s.Installer(osInfo)
	holders, err := ops.PortHolders(ctx, a.runner)
	if err != nil {
		return err
	}
	if len(holders) > 0 {
		return fmt.Errorf("ports 80 and 443 must be free for Caddy, but %s. Stop that web server (or move its sites into Caddy) and run setup again", strings.Join(holders, ", "))
	}
	v, installed, err := s.Caddy.Version(ctx)
	if err != nil {
		return err
	}
	if !installed {
		ok, err := a.term.confirm(fmt.Sprintf("Caddy isn't installed. Install it from %s?", inst.CaddySource()))
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("the manager needs Caddy for HTTPS")
		}
		a.say("  … installing Caddy")
		if err := inst.InstallCaddy(ctx); err != nil {
			return err
		}
		if v, _, err = s.Caddy.Version(ctx); err != nil {
			return err
		}
		a.log.Action("install caddy", nil)
	}
	if !caddy.VersionOK(v) {
		ok, err := a.term.confirm(fmt.Sprintf("Caddy %d.%d.%d is installed, probably %s's own package, but the manager needs %d.%d or newer. Upgrade it from %s? (Any sites it already serves keep working.)",
			v[0], v[1], v[2], osInfo.Name(), caddy.MinVersion[0], caddy.MinVersion[1], inst.CaddySource()))
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("Caddy %d.%d.%d is too old; the manager needs %d.%d or newer", v[0], v[1], v[2], caddy.MinVersion[0], caddy.MinVersion[1])
		}
		a.say("  … upgrading Caddy")
		if err := inst.InstallCaddy(ctx); err != nil {
			return err
		}
		if v, _, err = s.Caddy.Version(ctx); err != nil {
			return err
		}
		a.log.Action("upgrade caddy", nil)
		if !caddy.VersionOK(v) {
			return fmt.Errorf("Caddy is still %d.%d.%d after the upgrade; check whether the caddy package is pinned or held at %s's version", v[0], v[1], v[2], osInfo.Name())
		}
	}
	a.ok("Caddy %d.%d.%d", v[0], v[1], v[2])

	plan, err := s.PlanCaddy(v)
	if err != nil {
		return err
	}
	if plan.EditsOld != "" {
		a.say("Caddy already serves other sites. The manager will add two import lines to %s and change nothing else:\n\n%s", s.Caddy.Caddyfile, plan.NewMain)
		ok, err := a.term.confirm("Make this change?")
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("the manager needs its import lines in the Caddyfile")
		}
	}
	if err := s.ApplyCaddy(ctx, plan); err != nil {
		return err
	}
	a.ok("Caddy configuration (admin interface on a socket only Caddy and root can use)")
	if s.CaddyConfined() {
		// SELinux's web server policy keeps Caddy from connecting out: to
		// each server's port, and to Let's Encrypt.
		changed, err := s.AllowCaddyNetwork(ctx)
		if err != nil {
			return fmt.Errorf("let Caddy connect to the servers (SELinux boolean httpd_can_network_connect): %w", err)
		}
		if changed {
			a.log.Action("setsebool httpd_can_network_connect on", nil)
		}
		a.ok("SELinux lets Caddy connect to the servers (httpd_can_network_connect)")
	}
	return nil
}

func (a *App) setupFirewall(ctx context.Context, s *ops.Sys, osInfo distro.OS, noFirewall bool) error {
	name, err := firewall.Detect(ctx, a.runner)
	if err != nil {
		return err
	}
	if name == "" && !noFirewall {
		fw := osInfo.DefaultFirewall()
		ok, err := a.term.confirm(fmt.Sprintf("No firewall is on. Switch on %s (SSH stays open)?", fw))
		if err != nil {
			return err
		}
		if ok {
			program := map[string]string{"ufw": "ufw", "firewalld": "firewall-cmd"}[fw]
			if !haveProgram(program) {
				if err := s.Installer(osInfo).Install(ctx, fw); err != nil {
					return err
				}
			}
			ports := firewall.SSHPorts(ctx, a.runner)
			var enable func(context.Context, []int) error = firewall.UFW{R: a.runner}.Enable
			if fw == "firewalld" {
				enable = firewall.Firewalld{R: a.runner}.Enable
			}
			if err := enable(ctx, ports); err != nil {
				return err
			}
			a.log.Action("enable "+fw, nil)
			name = fw
			a.ok("%s switched on, with SSH (port %v) open", fw, ports)
		}
	}
	if name == "" {
		name = "none"
	}
	s.St.Settings.Firewall = name
	if err := s.EnsureCaddyRules(ctx); err != nil {
		return err
	}
	if name == "none" {
		a.warn("No firewall on this server. If your provider has a cloud firewall, open TCP 80 and 443, UDP 443, and TCP and UDP %d-%d there.",
			s.St.Settings.WebRTCPorts[0], s.St.Settings.WebRTCPorts[1])
	} else {
		a.ok("Firewall: %s, with 80/tcp, 443/tcp and 443/udp open for Caddy", name)
	}
	return nil
}

func (a *App) createVault(v *vault.Vault) error {
	fmt.Fprintln(a.Err, "The vault keeps secret tokens and data-folder keys. Choose a passphrase for it;\nit's separate from your OS password.")
	p, err := a.term.newPassphrase("vault passphrase")
	if err != nil {
		return err
	}
	key, err := v.Create(p)
	if err != nil {
		return err
	}
	a.log.Action("vault create", nil)
	a.term.secrets.AddSecret([]byte(key))

	fmt.Fprintf(a.Out, "\nRecovery key:\n\n    %s\n\n", key)
	fmt.Fprintln(a.Out, "This is shown once. Write it down or keep it in a password manager, away from\nthis server. It's the only way back into the vault, and into every backup, if\nthe passphrase is lost.")
	for {
		typed, err := a.term.visible("\nType the last 6 characters of the recovery key to confirm you've saved it: ", a.Globals.PassphraseStdin)
		if err != nil {
			return fmt.Errorf("the vault was created, but the recovery key wasn't confirmed: %w", err)
		}
		if vault.ConfirmRecoveryKey(key, typed) {
			break
		}
		if a.Globals.PassphraseStdin {
			return errors.New("the vault was created, but the recovery key confirmation didn't match")
		}
		fmt.Fprintln(a.Err, "That doesn't match. Check what you saved.")
	}
	a.term.clearScreen()
	return nil
}
