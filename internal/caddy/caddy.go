package caddy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

// Caddy manages the files and the service. Paths can point at a temp folder
// in tests.
type Caddy struct {
	R          run.Runner
	Caddyfile  string // /etc/caddy/Caddyfile
	GlobalFile string // /etc/caddy/<manager>/global.caddy
	SitesDir   string // /etc/caddy/sites
	DataDir    string // /var/lib/caddy/.local/share/caddy
	DropIn     string // UnitDropIn; empty skips EnsureUnit (tests)
}

func New(r run.Runner, globalFile string) *Caddy {
	return &Caddy{
		R:          r,
		Caddyfile:  "/etc/caddy/Caddyfile",
		GlobalFile: globalFile,
		SitesDir:   "/etc/caddy/sites",
		DataDir:    "/var/lib/caddy/.local/share/caddy",
		DropIn:     UnitDropIn,
	}
}

func (c *Caddy) SitesGlob() string         { return filepath.Join(c.SitesDir, "*.caddy") }
func (c *Caddy) SitePath(id string) string { return filepath.Join(c.SitesDir, id+".caddy") }
func (c *Caddy) RootCertPath() string {
	return filepath.Join(c.DataDir, "pki", "authorities", "local", "root.crt")
}
func (c *Caddy) certDir(issuer, d string) string {
	return filepath.Join(c.DataDir, "certificates", issuer, d)
}

// MinVersion is the oldest Caddy the manager is tested with: 2.10.0 is
// openSUSE Leap 16's (CI's end-to-end tests run on it); elsewhere setup
// installs the latest from the Caddy project.
var MinVersion = [3]int{2, 10, 0}

// Caddy's own builds print "v2.11.9 h1:...", Ubuntu's package just "2.6.2".
var versionRE = regexp.MustCompile(`(?m)^\s*v?(\d+)\.(\d+)\.(\d+)`)

// Version runs "caddy version". ok=false means Caddy isn't installed.
func (c *Caddy) Version(ctx context.Context) (v [3]int, ok bool, err error) {
	res, err := c.R.Run(ctx, run.Cmd{Name: "caddy", Args: []string{"version"}})
	if err != nil {
		if strings.Contains(err.Error(), "not installed") {
			return v, false, nil
		}
		return v, false, err
	}
	m := versionRE.FindStringSubmatch(string(res.Stdout))
	if m == nil {
		return v, true, fmt.Errorf("can't read Caddy's version from %q", strings.TrimSpace(string(res.Stdout)))
	}
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true, nil
}

// VersionOK reports whether v is at least MinVersion.
func VersionOK(v [3]int) bool {
	for i := range v {
		if v[i] != MinVersion[i] {
			return v[i] > MinVersion[i]
		}
	}
	return true
}

// ValidationError carries Caddy's own message.
type ValidationError struct{ Output string }

func (e *ValidationError) Error() string {
	return "Caddy refused the new configuration, so the previous one was put back:\n" + e.Output
}

// Validate checks the whole configuration as Caddy's own user. Run as root
// it would create a second certificate authority in root's home.
func (c *Caddy) Validate(ctx context.Context) error {
	res, err := c.R.Run(ctx, run.Cmd{
		Name: "runuser",
		Args: []string{"-u", "caddy", "--", "/usr/bin/caddy", "validate", "--config", c.Caddyfile, "--adapter", "caddyfile"},
		Env:  []string{"HOME=/var/lib/caddy"},
	})
	if err != nil {
		var exitErr *run.ExitError
		if errors.As(err, &exitErr) {
			return &ValidationError{Output: strings.TrimSpace(string(res.Stderr) + "\n" + string(res.Stdout))}
		}
		return err
	}
	return nil
}

// Reload applies the configuration. The packaged service reloads with
// --force, so it also retries certificates when nothing changed.
func (c *Caddy) Reload(ctx context.Context) error {
	_, err := c.R.Run(ctx, run.Cmd{Name: "systemctl", Args: []string{"reload", "caddy"}})
	return err
}

// Restart is needed once, when the admin interface moves to its socket.
func (c *Caddy) Restart(ctx context.Context) error {
	_, err := c.R.Run(ctx, run.Cmd{Name: "systemctl", Args: []string{"restart", "caddy"}})
	return err
}

// Apply writes files (path -> content; nil deletes), validates, and reloads.
// If validation fails every file is put back as it was.
func (c *Caddy) Apply(ctx context.Context, files map[string][]byte, restart bool) error {
	type backup struct {
		data    []byte
		existed bool
	}
	backups := map[string]backup{}
	for p := range files {
		b, err := os.ReadFile(p)
		backups[p] = backup{b, err == nil}
	}
	restore := func() {
		for p, b := range backups {
			if b.existed {
				_ = fsutil.WriteFileAtomic(p, b.data, 0o644, nil)
			} else {
				_ = fsutil.RemoveIfExists(p)
			}
		}
	}
	for p, data := range files {
		if data == nil {
			if err := fsutil.RemoveIfExists(p); err != nil {
				restore()
				return err
			}
			continue
		}
		if err := fsutil.MkdirAllMode(filepath.Dir(p), 0o755); err != nil {
			restore()
			return err
		}
		if err := fsutil.WriteFileAtomic(p, data, 0o644, nil); err != nil {
			restore()
			return err
		}
	}
	if err := c.Validate(ctx); err != nil {
		restore()
		return err
	}
	if restart {
		return c.Restart(ctx)
	}
	return c.Reload(ctx)
}

// UnitDropIn adjusts the packaged service where it differs from Caddy's
// own: openSUSE's starts with --resume (the last configuration set through
// the API wins over the Caddyfile after a restart) and reloads without
// --force (no certificate retry when nothing changed). Where SELinux
// confines Caddy, it also gives Caddy /run/caddy for its admin socket.
const UnitDropIn = "/etc/systemd/system/caddy.service.d/shiver-server-manager.conf"

// DropInFor returns the drop-in for a unit as "systemctl cat caddy" shows
// it, or nil when the unit already behaves like Caddy's own and needs no
// runtime folder. Sections from dropIn itself are skipped, so the answer
// doesn't depend on whether it's already there.
func DropInFor(unit, dropIn, marker string, runtimeDir bool) []byte {
	var start, reload string
	ours := false
	for _, l := range strings.Split(unit, "\n") {
		l = strings.TrimSpace(l)
		if p, ok := strings.CutPrefix(l, "# /"); ok {
			ours = "/"+p == dropIn
		}
		if ours {
			continue
		}
		if v, ok := strings.CutPrefix(l, "ExecStart="); ok && v != "" {
			start = v
		}
		if v, ok := strings.CutPrefix(l, "ExecReload="); ok && v != "" {
			reload = v
		}
	}
	exec := start != "" && (strings.Contains(start, "--resume") || !strings.Contains(reload, "--force"))
	if !exec && !runtimeDir {
		return nil
	}
	var b strings.Builder
	b.WriteString("# " + marker + "\n[Service]\n")
	if exec {
		b.WriteString("# Caddy starts from its Caddyfile, and a reload also retries certificates.\n" +
			"ExecStart=\nExecStart=/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile\n" +
			"ExecReload=\nExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile --force\n")
	}
	if runtimeDir {
		b.WriteString("# The admin socket's folder: SELinux lets Caddy make sockets in /run, not in /var/lib/caddy.\n" +
			"RuntimeDirectory=caddy\nRuntimeDirectoryMode=0700\n")
	}
	return []byte(b.String())
}

// EnsureUnit writes, updates or removes the drop-in as the packaged unit
// needs. changed means Caddy must restart to use it.
func (c *Caddy) EnsureUnit(ctx context.Context, marker string, runtimeDir bool) (changed bool, err error) {
	if c.DropIn == "" {
		return false, nil
	}
	res, err := c.R.Run(ctx, run.Cmd{Name: "systemctl", Args: []string{"cat", "caddy"}})
	if err != nil {
		return false, err
	}
	want := DropInFor(string(res.Stdout), c.DropIn, marker, runtimeDir)
	have, err := os.ReadFile(c.DropIn)
	switch {
	case err == nil && want != nil && bytes.Equal(have, want):
		return false, nil
	case want == nil && err != nil:
		return false, nil
	case want == nil:
		if err := fsutil.RemoveIfExists(c.DropIn); err != nil {
			return false, err
		}
	default:
		if err := fsutil.MkdirAllMode(filepath.Dir(c.DropIn), 0o755); err != nil {
			return false, err
		}
		if err := fsutil.WriteFileAtomic(c.DropIn, want, 0o644, nil); err != nil {
			return false, err
		}
	}
	_, err = c.R.Run(ctx, run.Cmd{Name: "systemctl", Args: []string{"daemon-reload"}})
	return err == nil, err
}

// FilesMatch reports whether every file already holds the given content
// (nil meaning absent).
func FilesMatch(files map[string][]byte) bool {
	for p, want := range files {
		if want == nil {
			if fsutil.Exists(p) {
				return false
			}
			continue
		}
		if !fsutil.SameContent(p, want) {
			return false
		}
	}
	return true
}

// Cert is a certificate's status.
type Cert struct {
	Domain   string        `json:"domain"`
	Issuer   string        `json:"issuer"`
	Present  bool          `json:"present"`
	NotAfter time.Time     `json:"notAfter,omitempty"`
	Lifetime time.Duration `json:"-"`
	DaysLeft int           `json:"daysLeft"`
}

// CertStatus reads the domain's certificate from Caddy's storage. A missing
// file means it's still pending.
func (c *Caddy) CertStatus(issuer, domain string, now time.Time) (Cert, error) {
	st := Cert{Domain: domain, Issuer: issuer}
	b, err := os.ReadFile(filepath.Join(c.certDir(issuer, domain), domain+".crt"))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return st, fmt.Errorf("the certificate file for %s isn't PEM", domain)
	}
	crt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return st, err
	}
	st.Present, st.NotAfter = true, crt.NotAfter
	st.Lifetime = crt.NotAfter.Sub(crt.NotBefore)
	st.DaysLeft = int(crt.NotAfter.Sub(now).Hours() / 24)
	return st, nil
}

// RenewalFailing reports whether less than a sixth of the lifetime is left.
// Caddy renews with a third left, so this means renewals keep failing.
func (s Cert) RenewalFailing(now time.Time) bool {
	return s.Present && s.Lifetime > 0 && s.NotAfter.Sub(now) < s.Lifetime/6
}

// DeleteCert removes a domain's certificate folder under one issuer.
func (c *Caddy) DeleteCert(issuer, domain string) error {
	if issuer == "" || domain == "" || strings.ContainsAny(issuer+domain, "/\\") || strings.Contains(domain, "..") {
		return fmt.Errorf("refusing to delete certificate folder %q/%q", issuer, domain)
	}
	return os.RemoveAll(c.certDir(issuer, domain))
}

// RootCert returns the self-signed root and its SHA-256 fingerprint.
func (c *Caddy) RootCert() (pemBytes []byte, fingerprint string, notAfter time.Time, err error) {
	pemBytes, err = os.ReadFile(c.RootCertPath())
	if err != nil {
		return nil, "", time.Time{}, err
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, "", time.Time{}, errors.New("the root certificate isn't PEM")
	}
	crt, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	sum := sha256.Sum256(block.Bytes)
	hexSum := strings.ToUpper(hex.EncodeToString(sum[:]))
	var parts []string
	for i := 0; i < len(hexSum); i += 2 {
		parts = append(parts, hexSum[i:i+2])
	}
	return pemBytes, strings.Join(parts, ":"), crt.NotAfter, nil
}

// LastCertError finds Caddy's most recent "could not get certificate"
// message for domain in its journal.
func (c *Caddy) LastCertError(ctx context.Context, domain string) (string, error) {
	res, err := c.R.Run(ctx, run.Cmd{Name: "journalctl", Args: []string{"-u", "caddy", "-o", "json", "--no-pager", "-n", "2000"}})
	if err != nil {
		return "", err
	}
	return lastCertError(res.Stdout, domain), nil
}

func lastCertError(journal []byte, domain string) string {
	var last string
	for _, line := range bytes.Split(journal, []byte("\n")) {
		var entry struct {
			Message string `json:"MESSAGE"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Message == "" {
			continue
		}
		var msg struct {
			Msg        string `json:"msg"`
			Identifier string `json:"identifier"`
			Error      string `json:"error"`
		}
		if json.Unmarshal([]byte(entry.Message), &msg) != nil {
			continue
		}
		if msg.Msg == "could not get certificate from issuer" && msg.Identifier == domain {
			last = msg.Error
		}
	}
	return last
}
