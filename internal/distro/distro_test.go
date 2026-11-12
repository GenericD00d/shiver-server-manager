package distro

import (
	"strings"
	"testing"
)

// os-release files as the distributions ship them (trimmed).
var releases = map[string]string{
	"ubuntu24":   "PRETTY_NAME=\"Ubuntu 24.04.5 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\nID=ubuntu\nID_LIKE=debian\nUBUNTU_CODENAME=noble\n",
	"ubuntu22":   "PRETTY_NAME=\"Ubuntu 22.04.5 LTS\"\nVERSION_ID=\"22.04\"\nID=ubuntu\nID_LIKE=debian\nUBUNTU_CODENAME=jammy\n",
	"mint22":     "NAME=\"Linux Mint\"\nVERSION_ID=\"22.2\"\nID=linuxmint\nID_LIKE=\"ubuntu debian\"\nPRETTY_NAME=\"Linux Mint 22.2\"\nUBUNTU_CODENAME=noble\n",
	"pop":        "NAME=\"Pop!_OS\"\nVERSION_ID=\"24.04\"\nID=pop\nID_LIKE=\"ubuntu debian\"\nPRETTY_NAME=\"Pop!_OS 24.04 LTS\"\nUBUNTU_CODENAME=noble\n",
	"debian13":   "PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nVERSION_ID=\"13\"\nVERSION_CODENAME=trixie\nID=debian\n",
	"debian11":   "PRETTY_NAME=\"Debian GNU/Linux 11 (bullseye)\"\nVERSION_ID=\"11\"\nID=debian\n",
	"fedora44":   "NAME=\"Fedora Linux\"\nVERSION_ID=44\nID=fedora\nPRETTY_NAME=\"Fedora Linux 44 (Server Edition)\"\n",
	"fedora41":   "NAME=\"Fedora Linux\"\nVERSION_ID=41\nID=fedora\nPRETTY_NAME=\"Fedora Linux 41\"\n",
	"alma9":      "NAME=\"AlmaLinux\"\nVERSION_ID=\"9.8\"\nID=\"almalinux\"\nID_LIKE=\"rhel centos fedora\"\nPRETTY_NAME=\"AlmaLinux 9.8 (Olive Jaguar)\"\n",
	"rocky10":    "NAME=\"Rocky Linux\"\nVERSION_ID=\"10.1\"\nID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\nPRETTY_NAME=\"Rocky Linux 10.1\"\n",
	"rhel8":      "NAME=\"Red Hat Enterprise Linux\"\nVERSION_ID=\"8.10\"\nID=\"rhel\"\nID_LIKE=\"fedora\"\nPRETTY_NAME=\"Red Hat Enterprise Linux 8.10 (Ootpa)\"\n",
	"tumbleweed": "NAME=\"openSUSE Tumbleweed\"\nID=\"opensuse-tumbleweed\"\nID_LIKE=\"opensuse suse\"\nVERSION_ID=\"20261110\"\nPRETTY_NAME=\"openSUSE Tumbleweed\"\n",
	"leap16":     "NAME=\"openSUSE Leap\"\nVERSION_ID=\"16.0\"\nID=\"opensuse-leap\"\nID_LIKE=\"suse opensuse\"\nPRETTY_NAME=\"openSUSE Leap 16.0\"\n",
	"leap15":     "NAME=\"openSUSE Leap\"\nVERSION_ID=\"15.6\"\nID=\"opensuse-leap\"\nID_LIKE=\"suse opensuse\"\nPRETTY_NAME=\"openSUSE Leap 15.6\"\n",
	"arch":       "NAME=\"Arch Linux\"\nPRETTY_NAME=\"Arch Linux\"\nID=arch\nBUILD_ID=rolling\n",
}

func TestCheckAndFamily(t *testing.T) {
	for name, tc := range map[string]struct {
		ok       bool
		family   Family
		format   string
		firewall string
	}{
		"ubuntu24":   {true, Debian, "deb", "ufw"},
		"ubuntu22":   {false, Debian, "deb", "ufw"},
		"mint22":     {true, Debian, "deb", "ufw"},
		"pop":        {true, Debian, "deb", "ufw"},
		"debian13":   {true, Debian, "deb", "ufw"},
		"debian11":   {false, Debian, "deb", "ufw"},
		"fedora44":   {true, Fedora, "rpm", "firewalld"},
		"fedora41":   {false, Fedora, "rpm", "firewalld"},
		"alma9":      {true, RHEL, "rpm", "firewalld"},
		"rocky10":    {true, RHEL, "rpm", "firewalld"},
		"rhel8":      {false, RHEL, "rpm", "firewalld"},
		"tumbleweed": {true, SUSE, "rpm", "firewalld"},
		"leap16":     {true, SUSE, "rpm", "firewalld"},
		"leap15":     {false, SUSE, "rpm", "firewalld"},
		"arch":       {false, "", "deb", "ufw"},
	} {
		o := Parse([]byte(releases[name]))
		err := o.Check()
		if (err == nil) != tc.ok {
			t.Errorf("%s: Check() = %v", name, err)
		}
		if err != nil && !strings.Contains(err.Error(), o.PrettyName) {
			t.Errorf("%s: %v doesn't name the system", name, err)
		}
		if o.Family() != tc.family || o.PackageFormat() != tc.format || o.DefaultFirewall() != tc.firewall {
			t.Errorf("%s: %q %q %q", name, o.Family(), o.PackageFormat(), o.DefaultFirewall())
		}
	}
}

func TestCaddyCOPRRepo(t *testing.T) {
	fedora := Parse([]byte(releases["fedora44"])).CaddyCOPRRepo()
	if !strings.Contains(fedora, "/@caddy/caddy/fedora-$releasever-$basearch/") || !strings.Contains(fedora, "gpgcheck=1") {
		t.Fatal(fedora)
	}
	alma := Parse([]byte(releases["alma9"])).CaddyCOPRRepo()
	if !strings.Contains(alma, "/@caddy/caddy/epel-9-$basearch/") {
		t.Fatal(alma)
	}
}

func TestDearmor(t *testing.T) {
	key, err := Dearmor([]byte("-----BEGIN PGP PUBLIC KEY BLOCK-----\nVersion: x\n\naGVsbG8=\n=abcd\n-----END PGP PUBLIC KEY BLOCK-----\n"))
	if err != nil || string(key) != "hello" {
		t.Fatalf("%q %v", key, err)
	}
	if _, err := Dearmor([]byte("<html>")); err == nil {
		t.Fatal("accepted a non-key")
	}
}
