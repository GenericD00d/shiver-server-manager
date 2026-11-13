package ops

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
	"github.com/GenericD00d/shiver-server-manager/internal/netinfo"
)

func keys(ns []Notice) string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Key)
	}
	return strings.Join(out, " ")
}

func TestHealth(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.s.Paths.HealthFile = filepath.Join(h.root, "health.json")
	h.sys.active["caddy.service"] = true
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	// Pretend Caddy got a certificate.
	certDir := filepath.Join(h.s.Caddy.DataDir, "certificates", "acme-v02.api.letsencrypt.org-directory", "chat-a.example.com")
	os.MkdirAll(certDir, 0o700)
	writeTestCert(t, filepath.Join(certDir, "chat-a.example.com.crt"))

	rep, appeared, _ := h.s.Health(ctx, HealthOpts{})
	for _, n := range rep.Notices {
		if n.Level != "info" {
			t.Errorf("healthy system reports %s: %s", n.Key, n.Message)
		}
	}
	h.s.SaveReport(rep)
	_ = appeared

	// Break things: the service dies, the voice port closes, the site goes.
	h.sys.active["sharkord@chat-a.service"] = false
	h.s.Firewall().Remove(ctx, firewall.Rule{Port: 40000, Proto: "udp", Instance: "chat-a"})
	os.Remove(h.s.Caddy.SitePath("chat-a"))
	rep, appeared, cleared := h.s.Health(ctx, HealthOpts{})
	got := keys(appeared)
	for _, want := range []string{"down:chat-a", "firewall:chat-a", "site:chat-a"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if len(cleared) != 0 {
		t.Fatalf("cleared %v", keys(cleared))
	}
	h.s.SaveReport(rep)

	// A deliberate stop isn't a problem; the fixed ones clear.
	h.s.Stop(ctx, "chat-a")
	h.s.Firewall().Allow(ctx, firewall.Rule{Port: 40000, Proto: "udp", Instance: "chat-a"})
	h.s.Caddy.Apply(ctx, map[string][]byte{h.s.Caddy.SitePath("chat-a"): h.s.siteFile("chat-a")}, false)
	_, appeared, cleared = h.s.Health(ctx, HealthOpts{})
	if len(appeared) != 0 || !strings.Contains(keys(cleared), "down:chat-a") || !strings.Contains(keys(cleared), "site:chat-a") {
		t.Fatalf("appeared %q cleared %q", keys(appeared), keys(cleared))
	}

	// A nearly full disk warns, whatever the disk the tests run on.
	h.s.DiskSpace = func(string) (uint64, uint64, error) { return 5 << 30, 1000 << 30, nil }
	rep, _, _ = h.s.Health(ctx, HealthOpts{})
	if !strings.Contains(keys(rep.Notices), "disk:backups") {
		t.Fatalf("no low-disk warning in %s", keys(rep.Notices))
	}
}

type fakeRouter struct {
	maps map[string]bool
	wan  string
}

func (f *fakeRouter) Map(_ context.Context, m netinfo.Mapping) (uint32, error) {
	f.maps[m.Proto+strconv.Itoa(m.Port)] = true
	return 0, nil
}
func (f *fakeRouter) Unmap(_ context.Context, port int, proto string) error {
	delete(f.maps, proto+strconv.Itoa(port))
	return nil
}
func (f *fakeRouter) ExternalIP(context.Context) (string, error) { return f.wan, nil }

func TestUPnPMappings(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	router := &fakeRouter{maps: map[string]bool{}}
	h.s.FindRouter = func(context.Context) (PortMapper, error) { return router, nil }
	h.s.St.Settings.UPnP = true
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	if err := h.s.UPnPMapAll(ctx); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"TCP" + strconv.Itoa(80), "TCP" + strconv.Itoa(443), "UDP" + strconv.Itoa(443), "UDP" + strconv.Itoa(40000), "TCP" + strconv.Itoa(40000)} {
		if !router.maps[k] {
			t.Errorf("missing mapping %s", k)
		}
	}
	if err := h.s.Delete(ctx, "chat-a"); err != nil {
		t.Fatal(err)
	}
	if router.maps["UDP"+strconv.Itoa(40000)] || router.maps["TCP"+strconv.Itoa(40000)] {
		t.Fatal("delete left the router mappings")
	}
	if p := netinfo.NATProblem("100.72.1.2", "203.0.113.10"); !strings.Contains(p, "carrier-grade") {
		t.Fatalf("CGNAT not spotted: %q", p)
	}
	if p := netinfo.NATProblem("192.168.0.2", "203.0.113.10"); !strings.Contains(p, "another router") {
		t.Fatalf("double NAT not spotted: %q", p)
	}
	if netinfo.NATProblem("203.0.113.10", "203.0.113.10") != "" {
		t.Fatal("a normal router flagged")
	}
}
