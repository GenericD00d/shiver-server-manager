// Package caddy writes Caddy's configuration as files under /etc/caddy and
// applies it with validate and reload. It never uses Caddy's admin API,
// since the packaged service reloads from the Caddyfile and would drop API
// changes.
package caddy

import (
	"bufio"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Global holds the manager's global options.
type Global struct {
	Email string // optional; Caddy then also uses ZeroSSL as a backup
	// ACMECA and ACMECARoot point Caddy at a test CA such as Pebble.
	ACMECA     string
	ACMECARoot string
	// AdminSocket moves Caddy's admin interface to a Unix socket.
	AdminSocket bool
}

// AdminSocketPath is where Caddy's admin interface listens.
const AdminSocketPath = "/var/lib/caddy/admin.sock"

// RenderGlobal is /etc/caddy/<manager>/global.caddy, imported into the
// Caddyfile's options block. The pki block names the self-signed root and
// keeps its certificates for a year; without it, "lifetime 365d" gives
// 7-day certificates.
func RenderGlobal(marker string, g Global) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s. Do not edit.\n", marker)
	if g.AdminSocket {
		fmt.Fprintf(&b, "admin unix/%s\n", AdminSocketPath)
	}
	if g.Email != "" {
		fmt.Fprintf(&b, "email %s\n", g.Email)
	}
	if g.ACMECA != "" {
		fmt.Fprintf(&b, "acme_ca %s\n", g.ACMECA)
		if g.ACMECARoot != "" {
			fmt.Fprintf(&b, "acme_ca_root %s\n", g.ACMECARoot)
		}
	}
	b.WriteString(`skip_install_trust
pki {
	ca local {
		name "Shiver Server Manager"
		root_cn "Shiver Server Manager Root"
		intermediate_lifetime 730d
		renewal_window_ratio 0.5
	}
}
`)
	return []byte(b.String())
}

// Site describes one instance's site file.
type Site struct {
	ID         string
	Domain     string
	Port       int
	SelfSigned bool
	TestMode   bool // Let's Encrypt staging
}

// StagingCA is Let's Encrypt's test server.
const StagingCA = "https://acme-staging-v02.api.letsencrypt.org/directory"

// RootCertPath is the self-signed root inside Caddy's data folder.
const RootCertPath = "/var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt"

// RenderSite is /etc/caddy/sites/<id>.caddy. While the instance is down,
// Caddy's 502 becomes a short maintenance page.
func RenderSite(marker string, s Site) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (instance %s). Do not edit.\n", marker, s.ID)
	fmt.Fprintf(&b, "%s {\n", s.Domain)
	switch {
	case s.SelfSigned:
		b.WriteString("\ttls {\n\t\tissuer internal {\n\t\t\tlifetime 365d\n\t\t}\n\t}\n")
	case s.TestMode:
		fmt.Fprintf(&b, "\ttls {\n\t\tca %s\n\t}\n", StagingCA)
	}
	fmt.Fprintf(&b, "\treverse_proxy 127.0.0.1:%d\n", s.Port)
	b.WriteString("\thandle_errors 502 {\n\t\trespond \"This server is offline for maintenance. Try again in a few minutes.\" 503\n\t}\n}\n")
	if s.SelfSigned {
		fmt.Fprintf(&b, `
http://%s {
	handle /sharkord-root.crt {
		root * /var/lib/caddy/.local/share/caddy/pki/authorities/local
		rewrite * /root.crt
		header Content-Type application/x-x509-ca-cert
		file_server
	}
	handle {
		redir https://{host}{uri} permanent
	}
}
`, s.Domain)
	}
	return []byte(b.String())
}

// IssuerDir is the folder name CertMagic stores an issuer's certificates
// under: the CA URL's host plus path, with "/" and ":" made "-".
func IssuerDir(caURL string) string {
	u, err := url.Parse(caURL)
	if err != nil || u.Host == "" {
		return ""
	}
	key := u.Host + strings.NewReplacer("/", "-", "\\", "-").Replace(u.Path)
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.NewReplacer(" ", "_", "+", "_plus_", "*", "wildcard_", ":", "-", "..", "").Replace(key)
	var out strings.Builder
	for _, r := range key {
		if r == '@' || r == '.' || r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Main Caddyfile layout.

const defaultMarker = "# The Caddyfile is an easy way to configure your Caddy web server."

// IsPackageDefault reports whether a Caddyfile is still the package's
// welcome page.
func IsPackageDefault(caddyfile string) bool {
	return strings.Contains(caddyfile, defaultMarker) && strings.Contains(caddyfile, "root * /usr/share/caddy") &&
		strings.Count(caddyfile, "{") == 1
}

// FreshCaddyfile is the main Caddyfile when the manager owns it.
func FreshCaddyfile(marker, globalFile, sitesGlob string) []byte {
	return []byte(fmt.Sprintf("# %s\n{\n\timport %s\n}\n\nimport %s\n", marker, globalFile, sitesGlob))
}

// AddImports adds the manager's two import lines to an admin's Caddyfile:
// the global file inside its options block (Caddy allows only one), or a new
// block at the top if it has none, and the sites line at the end. It returns
// the file unchanged if both are there.
func AddImports(caddyfile, globalFile, sitesGlob string) (string, error) {
	globalLine := "import " + globalFile
	sitesLine := "import " + sitesGlob
	hasGlobal := strings.Contains(caddyfile, globalLine)
	hasSites := strings.Contains(caddyfile, sitesLine)
	if hasGlobal && hasSites {
		return caddyfile, nil
	}
	out := caddyfile
	if !hasGlobal {
		lines := strings.Split(out, "\n")
		// The options block is a "{" alone on the first line that isn't
		// blank or a comment.
		first := -1
		for i, l := range lines {
			t := strings.TrimSpace(l)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			first = i
			break
		}
		if first >= 0 && strings.TrimSpace(lines[first]) == "{" {
			lines = append(lines[:first+1], append([]string{"\t" + globalLine}, lines[first+1:]...)...)
			out = strings.Join(lines, "\n")
		} else if first >= 0 && strings.HasPrefix(strings.TrimSpace(lines[first]), "{") {
			return "", errors.New("the Caddyfile's options block starts with something on the same line as \"{\"; add the import line by hand: " + globalLine)
		} else {
			out = "{\n\t" + globalLine + "\n}\n\n" + out
		}
	}
	if !hasSites {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += "\n" + sitesLine + "\n"
	}
	return out, nil
}

// HasImports reports whether both import lines are present.
func HasImports(caddyfile, globalFile, sitesGlob string) bool {
	sc := bufio.NewScanner(strings.NewReader(caddyfile))
	g, s := false, false
	for sc.Scan() {
		t := strings.TrimSpace(sc.Text())
		g = g || t == "import "+globalFile
		s = s || t == "import "+sitesGlob
	}
	return g && s
}
