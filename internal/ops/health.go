package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/caddy"
	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/netinfo"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/plugins"
	"github.com/GenericD00d/shiver-server-manager/internal/selfupdate"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
)

// Notice is one finding of the health check. Key is stable, so a problem
// can be told apart from a new one between runs.
type Notice struct {
	Key      string `json:"key"`
	Level    string `json:"level"` // "error", "warning", "info"
	Instance string `json:"instance,omitempty"`
	Message  string `json:"message"`
}

// Report is health.json.
type Report struct {
	CheckedAt time.Time `json:"checkedAt"`
	BootID    string    `json:"bootID"`
	Notices   []Notice  `json:"notices"`
	// Announced remembers what's been announced once (new versions).
	Announced []string `json:"announced,omitempty"`
}

// LoadReport reads the last health report.
func LoadReport(path string) Report {
	var r Report
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &r)
	}
	return r
}

func bootID() string {
	b, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(b))
}

// HealthOpts choose what the check may change.
type HealthOpts struct {
	// Unattended is the timer: it may follow a public IP change (rewrite env
	// files, restart, renew router mappings).
	Unattended bool
	// Online allows lookups that need the internet (public IP, releases).
	Online bool
}

// Health runs every check and returns the report; Changed lists notices
// that appeared or cleared since the last run.
func (s *Sys) Health(ctx context.Context, o HealthOpts) (rep Report, appeared, cleared []Notice) {
	prev := LoadReport(s.Paths.HealthFile)
	rep = Report{CheckedAt: s.Now().UTC(), BootID: bootID(), Announced: prev.Announced}
	add := func(level, key, id, format string, args ...any) {
		rep.Notices = append(rep.Notices, Notice{Key: key, Level: level, Instance: id, Message: fmt.Sprintf(format, args...)})
	}
	firstAfterBoot := prev.BootID != "" && prev.BootID != rep.BootID

	// Public address.
	if o.Online && !s.St.Settings.FixedPublicIPv4 {
		if ip, err := s.PublicIPv4(ctx); err != nil {
			add("warning", "public-ip", "", "couldn't check the public IPv4 address: %v", err)
		} else if ip != s.St.Settings.PublicIPv4 && s.St.Settings.PublicIPv4 != "" {
			old := s.St.Settings.PublicIPv4
			if o.Unattended {
				err := s.ChangePublicIP(ctx, ip)
				if s.St.Settings.UPnP {
					if uerr := s.UPnPMapAll(ctx); uerr != nil {
						err = errors.Join(err, uerr)
					}
				}
				if err != nil {
					add("error", "public-ip-change", "", "the public IPv4 address changed from %s to %s, but updating the servers failed: %v", old, ip, err)
				} else {
					add("info", "public-ip-changed:"+ip, "", "the public IPv4 address changed from %s to %s; servers now announce the new one. If your domains don't use dynamic DNS, they still point at the old address", old, ip)
				}
			} else {
				add("warning", "public-ip-change", "", "the public IPv4 address is now %s (servers announce %s); the health timer will update them, or run 'setup' now", ip, old)
			}
		}
	}

	// Caddy.
	if st, err := s.Systemd.Status(ctx, "caddy.service"); err != nil || st.Active != "active" {
		add("error", "caddy-down", "", "Caddy isn't running, so no server can be reached: systemctl status caddy")
	}
	fwRules, fwErr := s.Firewall().List(ctx)
	if fwErr != nil {
		add("warning", "firewall", "", "couldn't read the firewall rules: %v", fwErr)
	}

	for _, id := range s.IDs() {
		inst := s.St.Instances[id]
		if inst.Operation != nil {
			add("warning", "unfinished:"+id, id, "%s has an unfinished %s; run 'repair'", id, inst.Operation.Name)
			continue
		}
		locked := inst.Encrypted && !s.LUKS.IsMounted(ctx, inst.DataPath)
		switch {
		case locked:
			key := "locked:" + id
			if firstAfterBoot {
				key = "locked-after-boot:" + id // a new key, so an alert goes out after each reboot
			}
			add("info", key, id, "%s is encrypted and waits for an admin to log in and unlock the vault", id)
		case !inst.Stopped:
			st, err := s.Systemd.Status(ctx, systemd.Unit(id))
			switch {
			case err != nil:
				add("error", "down:"+id, id, "%s: can't read its service state: %v", id, err)
			case st.Active != "active":
				add("error", "down:"+id, id, "%s isn't running (%s); check its logs, or run 'repair'", id, st.Active)
			case s.Healthz(ctx, inst.HTTPPort) != nil:
				add("error", "not-answering:"+id, id, "%s is running but doesn't answer on its port", id)
			}
		}
		if inst.Encrypted && !locked {
			if used, total, err := s.ContainerUse(ctx, id); err == nil && total > 0 && used*100/total >= 80 {
				add("warning", "container-full:"+id, id, "%s's encrypted container is %d%% full; a full container stops Sharkord saving messages. Grow it with: instance grow %s --size <GiB>", id, used*100/total, id)
			}
		}
		// Certificate.
		if c, err := s.Caddy.CertStatus(s.Issuer(inst), inst.Domain, s.Now()); err == nil {
			switch {
			case !c.Present:
				add("warning", "cert-pending:"+id, id, "%s has no certificate yet; see 'cert status %s'", inst.Domain, id)
			case c.RenewalFailing(s.Now()):
				add("warning", "cert-renewal:"+id, id, "%s's certificate expires in %d days and Caddy's renewals are failing; see 'cert status %s'", inst.Domain, c.DaysLeft, id)
			}
		}
		// Site file and firewall rules.
		if !caddy.FilesMatch(map[string][]byte{s.Caddy.SitePath(id): s.siteFile(id)}) {
			add("error", "site:"+id, id, "%s's Caddy site file is missing or changed; run 'repair'", id)
		}
		if fwErr == nil && s.Firewall().Name() != "none" {
			for _, r := range firewall.InstanceRules(id, inst.WebRTCPort) {
				if !firewall.Contains(fwRules, r) {
					add("error", "firewall:"+id, id, "%s's voice port %d isn't open in the firewall; run 'firewall repair'", id, inst.WebRTCPort)
					break
				}
			}
		}
	}

	// Storage.
	for _, li := range s.Locations(ctx) {
		if li.Problem != "" {
			add("error", "location:"+li.Path, "", "storage location %s: %s", li.Path, li.Problem)
			continue
		}
		if free, total, err := s.DiskSpace(li.Path); err == nil && total > 0 && free*10 < total {
			add("warning", "disk:"+li.Path, "", "less than 10%% free at %s (%s left)", li.Path, humanBytes(free))
		}
	}
	if free, total, err := s.DiskSpace(s.St.Settings.BackupLocation); err == nil && total > 0 && free*10 < total {
		add("warning", "disk:backups", "", "less than 10%% free where backups go (%s)", s.St.Settings.BackupLocation)
	}

	// The self-signed root.
	if _, _, notAfter, err := s.Caddy.RootCert(); err == nil && notAfter.Sub(s.Now()) < RootWarnLeft {
		add("warning", "root-expiry", "", "the self-signed root certificate expires on %s; Caddy refuses to start once it has under 2 years left", notAfter.Format("2006-01-02"))
	}

	// New versions, announced once each.
	announce := func(key, format string, args ...any) {
		if !slices.Contains(rep.Announced, key) {
			rep.Announced = append(rep.Announced, key)
			add("info", key, "", format, args...)
		}
	}
	if o.Online {
		if latest, err := s.Sharkord.Latest(ctx); err == nil {
			for _, id := range s.IDs() {
				inst := s.St.Instances[id]
				if sharkord.CompareVersions(latest, inst.SharkordVersion) > 0 {
					announce("sharkord-"+latest+":"+id, "Sharkord %s is out; %s runs %s. Update with: instance update %s", latest, id, inst.SharkordVersion, id)
				}
			}
		}
	}
	if o.Online && s.Shiver != nil {
		if rel, err := s.Shiver.Latest(ctx); err == nil {
			for _, id := range s.IDs() {
				if m, ok := plugins.Installed(s.St.Instances[id].DataPath); ok && m.Version != rel.PluginVersion() {
					announce("shiver-"+rel.PluginVersion()+":"+id, "Shiver %s is out; %s has %s. Update with: shiver update %s", rel.PluginVersion(), id, m.Version, id)
				}
			}
		}
	}
	if o.Online && s.ManagerLatest != nil {
		if v, err := s.ManagerLatest(ctx); err == nil && selfupdate.Newer(v, s.ManagerVersion) {
			announce("manager-"+v, "Shiver Server Manager %s is out; this is %s. Update from System > Updates on the web page, or: sudo %s self-update", v, s.ManagerVersion, paths.Cmd)
		}
	}
	if o.Unattended && s.St.Settings.UPnP {
		if err := s.UPnPMapAll(ctx); err != nil {
			add("warning", "upnp", "", "couldn't renew the router's port forwarding: %v", err)
		}
	}

	sort.SliceStable(rep.Notices, func(i, j int) bool { return levelRank(rep.Notices[i].Level) < levelRank(rep.Notices[j].Level) })
	appeared, cleared = diffNotices(prev.Notices, rep.Notices)
	return rep, appeared, cleared
}

// SaveReport writes health.json.
func (s *Sys) SaveReport(r Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.Paths.HealthFile, b, 0o600, nil)
}

func levelRank(l string) int {
	switch l {
	case "error":
		return 0
	case "warning":
		return 1
	}
	return 2
}

func diffNotices(prev, cur []Notice) (appeared, cleared []Notice) {
	has := func(list []Notice, key string) bool {
		return slices.ContainsFunc(list, func(n Notice) bool { return n.Key == key })
	}
	for _, n := range cur {
		if !has(prev, n.Key) {
			appeared = append(appeared, n)
		}
	}
	for _, n := range prev {
		if !has(cur, n.Key) && n.Level != "info" {
			cleared = append(cleared, n)
		}
	}
	return
}

func humanBytes(b uint64) string {
	return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
}

// UPnPMapAll forwards Caddy's ports and every instance's voice port on the
// router.
func (s *Sys) UPnPMapAll(ctx context.Context) error {
	router, err := s.FindRouter(ctx)
	if err != nil {
		return err
	}
	var errs []error
	m := func(port int, proto, who string) netinfo.Mapping {
		return netinfo.Mapping{Port: port, Proto: proto, Label: paths.Cmd + " " + who}
	}
	maps := []netinfo.Mapping{m(80, "TCP", "caddy"), m(443, "TCP", "caddy"), m(443, "UDP", "caddy")}
	for _, id := range s.IDs() {
		if p := s.St.Instances[id].WebRTCPort; p != 0 {
			maps = append(maps, m(p, "UDP", id), m(p, "TCP", id))
		}
	}
	for _, m := range maps {
		if _, err := router.Map(ctx, m); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// UPnPUnmap removes an instance's mappings (on delete).
func (s *Sys) UPnPUnmap(ctx context.Context, port int) error {
	if !s.St.Settings.UPnP || port == 0 {
		return nil
	}
	router, err := s.FindRouter(ctx)
	if err != nil {
		return err
	}
	return errors.Join(router.Unmap(ctx, port, "UDP"), router.Unmap(ctx, port, "TCP"))
}
