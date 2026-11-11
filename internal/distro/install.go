package distro

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

// Installer installs packages with the system's package manager.
type Installer struct {
	R  run.Runner
	OS OS
}

const installTimeout = 10 * time.Minute

var aptEnv = []string{"DEBIAN_FRONTEND=noninteractive"}

// Install installs packages from the distribution's own repositories (ufw,
// firewalld, cryptsetup: the same names everywhere).
func (i Installer) Install(ctx context.Context, pkgs ...string) error {
	var c run.Cmd
	switch i.OS.Family() {
	case Fedora, RHEL:
		c = run.Cmd{Name: "dnf", Args: append([]string{"install", "-y"}, pkgs...)}
	case SUSE:
		c = run.Cmd{Name: "zypper", Args: append([]string{"--non-interactive", "install"}, pkgs...)}
	default:
		c = run.Cmd{Name: "apt-get", Args: append([]string{"install", "-y"}, pkgs...), Env: aptEnv}
	}
	c.Timeout = installTimeout
	_, err := i.R.Run(ctx, c)
	return err
}

// CaddySource names where InstallCaddy gets Caddy, for questions to the
// admin.
func (i Installer) CaddySource() string {
	switch i.OS.Family() {
	case Fedora, RHEL:
		return "the Caddy project's COPR repository"
	case SUSE:
		return "openSUSE's repositories"
	}
	return "Caddy's own apt repository"
}

// InstallCaddy installs Caddy, or upgrades an older one in place. The
// distributions' own Caddy is older than the manager needs on Debian,
// Ubuntu, Fedora and RHEL, so there it comes from the Caddy project's own
// repositories (https://caddyserver.com/docs/install). An existing
// Caddyfile is kept as it is (setup then only adds its import lines).
func (i Installer) InstallCaddy(ctx context.Context) error {
	switch i.OS.Family() {
	case Fedora, RHEL:
		return i.caddyDNF(ctx)
	case SUSE:
		for _, args := range [][]string{{"--non-interactive", "refresh"}, {"--non-interactive", "install", "caddy"}, {"--non-interactive", "update", "caddy"}} {
			if _, err := i.R.Run(ctx, run.Cmd{Name: "zypper", Args: args, Timeout: installTimeout}); err != nil {
				return err
			}
		}
		return nil
	}
	return i.caddyApt(ctx)
}

// Caddy's apt repository (https://caddyserver.com/docs/install#debian-ubuntu-raspbian).
const (
	CaddyKeyURL     = "https://dl.cloudsmith.io/public/caddy/stable/gpg.key"
	caddyKeyring    = "/usr/share/keyrings/caddy-stable-archive-keyring.gpg"
	caddySourceFile = "/etc/apt/sources.list.d/caddy-stable.list"
	caddySourceLine = "deb [signed-by=" + caddyKeyring + "] https://dl.cloudsmith.io/public/caddy/stable/deb/debian any-version main\n"
)

// The Caddy project's COPR (https://caddyserver.com/docs/install#fedora-redhat-centos),
// written out as the .repo file "dnf copr enable @caddy/caddy" would add,
// so no dnf plugin is needed.
const (
	CaddyCOPRKeyURL = "https://download.copr.fedorainfracloud.org/results/@caddy/caddy/pubkey.gpg"
	caddyRepoFile   = "/etc/yum.repos.d/_copr:copr.fedorainfracloud.org:group_caddy:caddy.repo"
)

// CaddyCOPRRepo is the .repo file for this system: Fedora's own release, or
// EPEL's for RHEL and its rebuilds.
func (o OS) CaddyCOPRRepo() string {
	chroot := "fedora-$releasever"
	if o.Family() == RHEL {
		chroot = fmt.Sprintf("epel-%d", o.major())
	}
	return "# Added by " + paths.Cmd + "\n" +
		"[copr:copr.fedorainfracloud.org:group_caddy:caddy]\n" +
		"name=Copr repo for caddy owned by @caddy\n" +
		"baseurl=https://download.copr.fedorainfracloud.org/results/@caddy/caddy/" + chroot + "-$basearch/\n" +
		"type=rpm-md\nskip_if_unavailable=True\ngpgcheck=1\n" +
		"gpgkey=" + CaddyCOPRKeyURL + "\n" +
		"repo_gpgcheck=0\nenabled=1\nenabled_metadata=1\n"
}

func (i Installer) caddyDNF(ctx context.Context) error {
	if err := fsutil.WriteFileAtomic(caddyRepoFile, []byte(i.OS.CaddyCOPRRepo()), 0o644, nil); err != nil {
		return err
	}
	// install leaves an older Caddy (EPEL's) alone; upgrade then moves it
	// to the COPR's.
	for _, args := range [][]string{{"install", "-y", "caddy"}, {"upgrade", "-y", "caddy"}} {
		if _, err := i.R.Run(ctx, run.Cmd{Name: "dnf", Args: args, Timeout: installTimeout}); err != nil {
			return err
		}
	}
	return nil
}

// Dearmor turns an ASCII-armoured OpenPGP key into the binary form apt's
// signed-by keyrings take, without needing gpg.
func Dearmor(armored []byte) ([]byte, error) {
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

func (i Installer) caddyApt(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, CaddyKeyURL, nil)
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
	key, err := Dearmor(armored)
	if err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(caddyKeyring, key, 0o644, nil); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(caddySourceFile, []byte("# Added by "+paths.Cmd+"\n"+caddySourceLine), 0o644, nil); err != nil {
		return err
	}
	if _, err := i.R.Run(ctx, run.Cmd{Name: "apt-get", Args: []string{"update"}, Env: aptEnv, Timeout: installTimeout}); err != nil {
		return err
	}
	// Also upgrades the distribution's older Caddy in place.
	_, err = i.R.Run(ctx, run.Cmd{Name: "apt-get", Args: []string{"install", "-y",
		"-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "caddy"},
		Env: aptEnv, Timeout: installTimeout})
	return err
}

// CaddyRepoURL is a URL setup checks it can reach before installing Caddy.
func (o OS) CaddyRepoURL() (name, url string) {
	switch o.Family() {
	case Fedora, RHEL:
		return "Caddy's COPR repository", CaddyCOPRKeyURL
	case SUSE:
		return "openSUSE's repositories", "https://download.opensuse.org/"
	}
	return "Caddy's apt repository", CaddyKeyURL
}
