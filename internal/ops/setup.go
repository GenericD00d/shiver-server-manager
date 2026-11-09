package ops

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/caddy"
	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

// OSInfo is what /etc/os-release says.
type OSInfo struct {
	ID, VersionID, VersionCodename, UbuntuCodename, PrettyName string
}

func ReadOS(path string) (OSInfo, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return OSInfo{}, err
	}
	o := OSInfo{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			o.ID = v
		case "VERSION_ID":
			o.VersionID = v
		case "VERSION_CODENAME":
			o.VersionCodename = v
		case "UBUNTU_CODENAME":
			o.UbuntuCodename = v
		case "PRETTY_NAME":
			o.PrettyName = v
		}
	}
	return o, nil
}

// Supported checks for Ubuntu 24.04 or 26.04, or Linux Mint 22 (built on
// 24.04). Mint names its Ubuntu base in UBUNTU_CODENAME, so that's what
// counts rather than Mint's own version.
func (o OSInfo) Supported() error {
	switch {
	case o.ID == "ubuntu" && (o.VersionID == "24.04" || o.VersionID == "26.04"):
		return nil
	case o.ID == "linuxmint" && o.UbuntuCodename == "noble":
		return nil
	}
	return fmt.Errorf("%s isn't supported; use Ubuntu 24.04 or 26.04, or Linux Mint 22", o.PrettyName)
}

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

// Caddy's apt repository (https://caddyserver.com/docs/install#debian-ubuntu-raspbian).
const (
	caddyKeyURL     = "https://dl.cloudsmith.io/public/caddy/stable/gpg.key"
	caddyKeyring    = "/usr/share/keyrings/caddy-stable-archive-keyring.gpg"
	caddySourceFile = "/etc/apt/sources.list.d/caddy-stable.list"
	caddySourceLine = "deb [signed-by=" + caddyKeyring + "] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main\n"
)

// dearmor turns an ASCII-armoured OpenPGP key into the binary form apt's
// signed-by keyrings take, without needing gpg.
func dearmor(armored []byte) ([]byte, error) {
	var body strings.Builder
	in := false
	for _, line := range strings.Split(strings.ReplaceAll(string(armored), "\r", ""), "\n") {
		switch {
		case strings.HasPrefix(line, "-----BEGIN PGP PUBLIC KEY BLOCK-----"):
			in = true
		case strings.HasPrefix(line, "-----END PGP"):
			in = false
		case !in:
		case strings.Contains(line, ": "), line == "":
			// armour headers and the blank line after them
		case strings.HasPrefix(line, "="):
			// CRC24 checksum line
		default:
			body.WriteString(strings.TrimSpace(line))
		}
	}
	key, err := base64.StdEncoding.DecodeString(body.String())
	if err != nil || len(key) == 0 {
		return nil, errors.New("Caddy's signing key isn't a valid armoured OpenPGP key")
	}
	return key, nil
}

// InstallCaddy adds Caddy's apt repository and installs Caddy from it.
// Ubuntu's own Caddy (2.6) is too old for the self-signed settings.
func (s *Sys) InstallCaddy(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, caddyKeyURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download Caddy's signing key: %w", err)
	}
	defer resp.Body.Close()
	armored, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil || resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download Caddy's signing key: %v %s", err, resp.Status)
	}
	key, err := dearmor(armored)
	if err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(caddyKeyring, key, 0o644, nil); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(caddySourceFile, []byte("# Added by "+paths.Cmd+"\n"+caddySourceLine), 0o644, nil); err != nil {
		return err
	}
	apt := []string{"DEBIAN_FRONTEND=noninteractive"}
	if _, err := s.R.Run(ctx, run.Cmd{Name: "apt-get", Args: []string{"update"}, Env: apt, Timeout: 10 * time.Minute}); err != nil {
		return err
	}
	_, err = s.R.Run(ctx, run.Cmd{Name: "apt-get", Args: []string{"install", "-y", "caddy"}, Env: apt, Timeout: 10 * time.Minute})
	return err
}

// AptInstall installs packages from Ubuntu's archive (ufw, cryptsetup).
func (s *Sys) AptInstall(ctx context.Context, pkgs ...string) error {
	_, err := s.R.Run(ctx, run.Cmd{Name: "apt-get", Args: append([]string{"install", "-y"}, pkgs...),
		Env: []string{"DEBIAN_FRONTEND=noninteractive"}, Timeout: 10 * time.Minute})
	return err
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
func (s *Sys) PlanCaddy() (CaddyPlan, error) {
	c := s.Caddy
	g := caddy.Global{AdminSocket: true, Email: s.St.Settings.Email, ACMECA: s.St.Settings.ACMECA, ACMECARoot: s.St.Settings.ACMECARoot}
	global := caddy.RenderGlobal(paths.Marker, g)
	plan := CaddyPlan{Files: map[string][]byte{c.GlobalFile: global}}
	old, _ := os.ReadFile(c.GlobalFile)
	plan.Restart = !bytes.Contains(old, []byte("admin unix/"))

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
func Outbound(ctx context.Context) map[string]error {
	sites := map[string]string{
		"GitHub (Sharkord and Shiver downloads)": "https://api.github.com",
		"Let's Encrypt":                          "https://acme-v02.api.letsencrypt.org/directory",
		"Caddy's apt repository":                 "https://dl.cloudsmith.io/public/caddy/stable/gpg.key",
	}
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

// Auto-updates: Ubuntu's unattended-upgrades only covers Ubuntu's own
// archive; Caddy's repository is added to it by origin.

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
