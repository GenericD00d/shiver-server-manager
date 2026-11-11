// Package distro tells the Linux distributions the manager runs on apart:
// which are supported, how they install packages, which firewall they
// usually use, and which kind of package the manager itself comes in.
//
// Arch isn't supported: it no longer packages Caddy.
package distro

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// Family groups distributions that install software the same way.
type Family string

const (
	Debian Family = "debian" // Debian, Ubuntu, Mint: apt and .deb
	Fedora Family = "fedora" // Fedora: dnf and .rpm
	RHEL   Family = "rhel"   // RHEL, AlmaLinux, Rocky, CentOS Stream: dnf, EPEL-style
	SUSE   Family = "suse"   // openSUSE: zypper and .rpm
)

// OS is what /etc/os-release says.
type OS struct {
	ID, VersionID, VersionCodename, UbuntuCodename, PrettyName string
	IDLike                                                     []string
}

// Read parses an os-release file.
func Read(path string) (OS, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return OS{}, err
	}
	return Parse(b), nil
}

// Parse reads os-release contents.
func Parse(b []byte) OS {
	o := OS{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"'`)
		switch k {
		case "ID":
			o.ID = v
		case "ID_LIKE":
			o.IDLike = strings.Fields(v)
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
	if o.PrettyName == "" {
		o.PrettyName = strings.TrimSpace(o.ID + " " + o.VersionID)
	}
	return o
}

// Ubuntu releases the manager is tested on, by codename, so that Mint and
// other Ubuntu-based systems count by the Ubuntu they're built on.
var ubuntuCodenames = map[string]string{"noble": "24.04", "resolute": "26.04"}

var rhelIDs = []string{"rhel", "almalinux", "rocky", "centos", "ol"}

func (o OS) like(id string) bool { return o.ID == id || slices.Contains(o.IDLike, id) }

// Family is the distribution's family, or "" for one the manager doesn't
// know.
func (o OS) Family() Family {
	switch {
	case o.ID == "fedora":
		return Fedora
	case slices.Contains(rhelIDs, o.ID):
		return RHEL
	case o.like("debian") || o.like("ubuntu"):
		return Debian
	case strings.HasPrefix(o.ID, "opensuse") || o.like("suse") || o.like("opensuse"):
		return SUSE
	case o.like("rhel") || o.like("centos"):
		return RHEL
	case o.like("fedora"):
		return Fedora
	}
	return ""
}

// Name is the distribution's name without its version, for messages
// ("Ubuntu", "Fedora Linux").
func (o OS) Name() string {
	names := map[string]string{
		"ubuntu": "Ubuntu", "debian": "Debian", "raspbian": "Raspberry Pi OS", "linuxmint": "Linux Mint",
		"fedora": "Fedora", "rhel": "RHEL", "almalinux": "AlmaLinux", "rocky": "Rocky Linux", "centos": "CentOS",
		"ol": "Oracle Linux", "opensuse-leap": "openSUSE", "opensuse-tumbleweed": "openSUSE", "opensuse-slowroll": "openSUSE",
	}
	if n, ok := names[o.ID]; ok {
		return n
	}
	return o.PrettyName
}

// major is the number before the first dot of VERSION_ID, or -1.
func (o OS) major() int {
	n, err := strconv.Atoi(strings.SplitN(o.VersionID, ".", 2)[0])
	if err != nil {
		return -1
	}
	return n
}

// Supported lists what the manager runs on, as a sentence for messages.
const Supported = "Ubuntu 24.04 or 26.04 (or Linux Mint 22), Debian 12 or 13, Fedora 43 or newer, RHEL, AlmaLinux or Rocky Linux 9 or 10, or openSUSE Tumbleweed or Leap 16"

// Check returns an error unless the manager supports this system.
func (o OS) Check() error {
	ok := false
	switch {
	case o.ID == "ubuntu":
		ok = o.VersionID == "24.04" || o.VersionID == "26.04"
	case o.UbuntuCodename != "":
		// Mint, Pop!_OS, Zorin and the like: their Ubuntu base counts.
		_, ok = ubuntuCodenames[o.UbuntuCodename]
	case o.ID == "debian" || o.ID == "raspbian":
		ok = o.major() == 12 || o.major() == 13
	case o.ID == "fedora":
		ok = o.major() >= 43
	case o.Family() == RHEL:
		ok = o.major() == 9 || o.major() == 10
	case o.ID == "opensuse-tumbleweed" || o.ID == "opensuse-slowroll":
		ok = true
	case o.ID == "opensuse-leap":
		ok = o.major() >= 16
	}
	if !ok {
		return fmt.Errorf("%s isn't supported; the manager runs on %s", o.PrettyName, Supported)
	}
	return nil
}

// PackageFormat is how the manager itself is packaged for this system:
// "deb" or "rpm".
func (o OS) PackageFormat() string {
	switch o.Family() {
	case Fedora, RHEL, SUSE:
		return "rpm"
	}
	return "deb"
}

// PackageManager is "apt", "dnf" or "zypper".
func (o OS) PackageManager() string {
	switch o.Family() {
	case Fedora, RHEL:
		return "dnf"
	case SUSE:
		return "zypper"
	}
	return "apt"
}

// DefaultFirewall is the firewall setup offers to switch on when none is:
// ufw on Debian and Ubuntu, firewalld elsewhere.
func (o OS) DefaultFirewall() string {
	if o.Family() == Debian || o.Family() == "" {
		return "ufw"
	}
	return "firewalld"
}

// UninstallCommand is how an admin removes the manager's package.
func (o OS) UninstallCommand(pkg string) string {
	switch o.Family() {
	case Fedora, RHEL:
		return "sudo dnf remove " + pkg
	case SUSE:
		return "sudo zypper remove " + pkg
	}
	return "sudo apt remove " + pkg
}
