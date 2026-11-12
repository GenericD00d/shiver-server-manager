package ops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/caddy"
	"github.com/GenericD00d/shiver-server-manager/internal/distro"
	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

// CaddyConfined reports whether SELinux confines Caddy (see
// caddy.Confined); tests can't be.
func (s *Sys) CaddyConfined() bool { return s.Caddy.DropIn != "" && caddy.Confined() }

// OS reads /etc/os-release.
func OS() (distro.OS, error) { return distro.Read("/etc/os-release") }

// Installer installs packages the way this system does.
func (s *Sys) Installer(o distro.OS) distro.Installer { return distro.Installer{R: s.R, OS: o} }

// PortHolders lists programs other than Caddy listening on TCP 80 or 443.
func PortHolders(ctx context.Context, r run.Runner) ([]string, error) {
	res, err := r.Run(ctx, run.Cmd{Name: "ss", Args: []string{"-H", "-ltnp"}})
	if err != nil {
		return nil, err
	}
	return parsePortHolders(string(res.Stdout)), nil
}

var ssProc = regexp.MustCompile(`users:\(\("([^"]+)"`)

func parsePortHolders(out string) []string {
	var holders []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		local := f[3]
		if !strings.HasSuffix(local, ":80") && !strings.HasSuffix(local, ":443") {
			continue
		}
		name := "an unknown program"
		if m := ssProc.FindStringSubmatch(line); m != nil {
			name = m[1]
		}
		if name == "caddy" {
			continue
		}
		holders = append(holders, fmt.Sprintf("%s on %s", name, local))
	}
	return holders
}

// CaddyPlan is the change setup will make to Caddy's configuration.
type CaddyPlan struct {
	Files    map[string][]byte
	Restart  bool   // the admin interface moves to its socket
	EditsOld string // the admin's own Caddyfile before the edit, if edited
	NewMain  string
}

// PlanCaddy works out the main Caddyfile and the global file. An unchanged
// package default is replaced; an admin's own Caddyfile only gets the two
// import lines.
func (s *Sys) PlanCaddy(version [3]int) (CaddyPlan, error) {
	c := s.Caddy
	sock := caddy.SocketPath(s.CaddyConfined())
	g := caddy.Global{AdminSocket: true, Email: s.St.Settings.Email, ACMECA: s.St.Settings.ACMECA, ACMECARoot: s.St.Settings.ACMECARoot,
		NoRenewalRatio: version[0] == 2 && version[1] < 11, SocketPath: sock}
	global := caddy.RenderGlobal(paths.Marker, g)
	plan := CaddyPlan{Files: map[string][]byte{c.GlobalFile: global}}
	old, _ := os.ReadFile(c.GlobalFile)
	// A reload looks for the new socket before Caddy has opened it, so a
	// move needs a restart.
	plan.Restart = !bytes.Contains(old, []byte("admin unix/"+sock+"\n"))

	main, err := os.ReadFile(c.Caddyfile)
	switch {
	case errors.Is(err, os.ErrNotExist) || caddy.IsPackageDefault(string(main)) || bytes.HasPrefix(main, []byte("# "+paths.Marker)):
		plan.NewMain = string(caddy.FreshCaddyfile(paths.Marker, c.GlobalFile, c.SitesGlob()))
	case err != nil:
		return plan, err
	default:
		edited, err := caddy.AddImports(string(main), c.GlobalFile, c.SitesGlob())
		if err != nil {
			return plan, err
		}
		plan.NewMain = edited
		if edited != string(main) {
			plan.EditsOld = string(main)
		}
	}
	plan.Files[c.Caddyfile] = []byte(plan.NewMain)
	return plan, nil
}

// ApplyCaddy writes the plan, validates and reloads (or restarts).
func (s *Sys) ApplyCaddy(ctx context.Context, plan CaddyPlan) error {
	if err := fsutil.MkdirAllMode(s.Caddy.SitesDir, 0o755); err != nil {
		return err
	}
	if changed, err := s.Caddy.EnsureUnit(ctx, paths.Marker, s.CaddyConfined()); err != nil {
		return err
	} else if changed {
		plan.Restart = true
	}
	if caddy.FilesMatch(plan.Files) && !plan.Restart {
		return nil
	}
	return s.Caddy.Apply(ctx, plan.Files, plan.Restart)
}

// EnsureCaddyRules opens 80, 443 and 443/udp.
func (s *Sys) EnsureCaddyRules(ctx context.Context) error {
	for _, r := range firewall.CaddyRules {
		if err := s.Firewall().Allow(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// ClockSynced asks timedatectl whether NTP has synchronised the clock.
func (s *Sys) ClockSynced(ctx context.Context) (bool, error) {
	res, err := s.R.Run(ctx, run.Cmd{Name: "timedatectl", Args: []string{"show", "-p", "NTPSynchronized", "--value"}})
	return strings.TrimSpace(string(res.Stdout)) == "yes", err
}

// Outbound checks the sites setup and running need.
func Outbound(ctx context.Context, o distro.OS) map[string]error {
	sites := map[string]string{
		"GitHub (Sharkord and Shiver downloads)": "https://api.github.com",
		"Let's Encrypt":                          "https://acme-v02.api.letsencrypt.org/directory",
	}
	name, u := o.CaddyRepoURL()
	sites[name] = u
	out := map[string]error{}
	client := &http.Client{Timeout: 10 * time.Second}
	for name, u := range sites {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		out[name] = err
	}
	return out
}

// Auto-updates (Debian and Ubuntu): unattended-upgrades only covers the
// distribution's own archive; Caddy's repository is added to it by origin.
// dnf-automatic and openSUSE's updates cover every repository already.

const autoUpgradesFile = "/etc/apt/apt.conf.d/20auto-upgrades"

func AutoUpdatesOn() bool {
	b, err := os.ReadFile(autoUpgradesFile)
	return err == nil && regexp.MustCompile(`APT::Periodic::Unattended-Upgrade\s+"1"`).Match(b)
}

var releaseOrigin = regexp.MustCompile(`release\s+.*\bo=([^,\s]+)`)

// caddyOrigin finds the "o=" value of Caddy's repository in
// "apt-cache policy caddy".
func caddyOrigin(policy string) string {
	lines := strings.Split(policy, "\n")
	for i, l := range lines {
		if strings.Contains(l, "dl.cloudsmith.io/public/caddy") && i+1 < len(lines) {
			if m := releaseOrigin.FindStringSubmatch(lines[i+1]); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// AddCaddyToAutoUpdates writes apt.conf.d/52<manager> with Caddy's origin.
func (s *Sys) AddCaddyToAutoUpdates(ctx context.Context) error {
	res, err := s.R.Run(ctx, run.Cmd{Name: "apt-cache", Args: []string{"policy", "caddy"}})
	if err != nil {
		return err
	}
	origin := caddyOrigin(string(res.Stdout))
	if origin == "" {
		return errors.New("couldn't find Caddy's repository in apt-cache policy")
	}
	conf := fmt.Sprintf("// %s\nUnattended-Upgrade::Origins-Pattern {\n    \"origin=%s\";\n};\n", paths.Marker, origin)
	return fsutil.WriteFileAtomic(filepath.Join("/etc/apt/apt.conf.d", "52"+paths.Cmd), []byte(conf), 0o644, nil)
}
