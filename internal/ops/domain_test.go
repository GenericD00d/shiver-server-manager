package ops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/netinfo"
)

func TestChangeDomain(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	h.s.St.Settings.ACMECA = "https://localhost:14000/dir" // no public DNS check
	iss := filepath.Join(h.s.Caddy.DataDir, "certificates", "localhost-14000-dir")
	for _, d := range []string{"chat-a.example.com", "talk.example.com"} {
		os.MkdirAll(filepath.Join(iss, d), 0o700)
		writeTestCert(t, filepath.Join(iss, d, d+".crt"))
	}
	if err := h.s.ChangeDomain(ctx, "chat-a", "Talk.Example.com", false); err != nil {
		t.Fatal(err)
	}
	inst := h.s.St.Instances["chat-a"]
	if inst.Domain != "talk.example.com" || inst.Operation != nil {
		t.Fatalf("%+v", inst)
	}
	site, _ := os.ReadFile(h.s.Caddy.SitePath("chat-a"))
	if !strings.Contains(string(site), "talk.example.com {") || strings.Contains(string(site), "chat-a.example.com") {
		t.Fatalf("site:\n%s", site)
	}
	if _, err := os.Stat(h.s.nextSitePath("chat-a")); err == nil {
		t.Fatal(".next site file left")
	}
	if _, err := os.Stat(filepath.Join(iss, "chat-a.example.com")); err == nil {
		t.Fatal("old certificate left")
	}
	if err := h.s.Create(ctx, req("chat-b")); err != nil {
		t.Fatal(err)
	}
	if err := h.s.ChangeDomain(ctx, "chat-b", "talk.example.com", false); err == nil {
		t.Fatal("a domain in use was accepted")
	}
}

func TestLimits(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	limits := filepath.Join(h.s.dropinDir("chat-a"), "limits.conf")
	if err := h.s.SetLimits(ctx, "chat-a", 2048, 150); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(limits)
	if !strings.Contains(string(b), "MemoryMax=2048M") || !strings.Contains(string(b), "MemoryHigh=1843M") || !strings.Contains(string(b), "CPUQuota=150%") {
		t.Fatalf("limits.conf:\n%s", b)
	}
	if err := h.s.SetLimits(ctx, "chat-a", 100, 0); err == nil {
		t.Fatal("tiny memory cap accepted")
	}
	if err := h.s.SetLimits(ctx, "chat-a", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(limits); err == nil {
		t.Fatal("limits.conf left after removing the caps")
	}
}

func TestSkipDNSCheckForAProxy(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.s.DNSCheck = func(_ context.Context, d string) netinfo.DNSResult {
		return netinfo.DNSResult{Domain: d, Problem: d + " points at 198.51.100.9 (the proxy), not this server"}
	}
	var notes []string
	h.s.Progress = func(e Event) {
		if e.Status == "note" {
			notes = append(notes, e.Detail)
		}
	}
	if err := h.s.Create(ctx, req("plain")); err != nil {
		t.Fatal(err)
	}
	if len(notes) == 0 || !strings.Contains(strings.Join(notes, " "), "points at") {
		t.Fatalf("no DNS warning without the option: %q", notes)
	}
	if _, err := h.s.RetryCert(ctx, "plain"); err == nil {
		t.Fatal("retry ignored the DNS problem")
	}

	notes = nil
	r := req("proxied")
	r.SkipDNSCheck = true
	if err := h.s.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(notes, " "), "points at") {
		t.Fatalf("DNS checked anyway: %q", notes)
	}
	if !h.s.St.Instances["proxied"].SkipDNSCheck {
		t.Fatal("the option wasn't kept in the record")
	}
	if _, err := h.s.RetryCert(ctx, "proxied"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if err := h.s.ChangeDomain(ctx, "proxied", "new.example.com", false); err != nil {
		t.Fatalf("change domain: %v", err)
	}
}
