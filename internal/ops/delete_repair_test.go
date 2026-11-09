package ops

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

func TestDeleteLeavesNothing(t *testing.T) {
	h := newHarness(t)
	before := fileList(h.root)
	if err := h.s.Create(context.Background(), req("chat-a")); err != nil {
		t.Fatal(err)
	}
	if err := h.s.Delete(context.Background(), "chat-a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.s.St.Instances["chat-a"]; ok {
		t.Fatal("record left")
	}
	if h.sys.users["sharkord-chat-a"] || h.sys.enabled["sharkord@chat-a.service"] {
		t.Fatal("user or service left")
	}
	if h.s.Vault.HasAnySecret("chat-a") {
		t.Fatal("secrets left")
	}
	rules, _ := h.s.Firewall().List(context.Background())
	if len(rules) != len(firewall.CaddyRules) {
		t.Fatalf("firewall rules left: %v", rules)
	}
	// Only the downloaded Sharkord binary and the records remain.
	for _, p := range fileList(h.root) {
		if strings.HasPrefix(p, "opt/") || p == "state.json" || slices.Contains(before, p) {
			continue
		}
		t.Errorf("left behind: %s", p)
	}
	if err := h.s.Delete(context.Background(), "chat-a"); err == nil {
		t.Fatal("deleting a deleted server succeeded")
	}
}

func TestDeleteResumesAfterEveryStep(t *testing.T) {
	steps := len(newHarness(t).s.deleteSteps2())
	for stopAfter := 1; stopAfter < steps; stopAfter++ {
		t.Run(fmt.Sprint(stopAfter), func(t *testing.T) {
			h := newHarness(t)
			if err := h.s.Create(context.Background(), req("chat-a")); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := 0
			h.s.Progress = func(e Event) {
				if e.Op == OpDelete && (e.Status == "done" || e.Status == "skip") {
					if done++; done == stopAfter {
						cancel()
					}
				}
			}
			if err := h.s.Delete(ctx, "chat-a"); !IsPartial(err) {
				t.Fatalf("got %v", err)
			}
			st, _ := state.Load(h.s.Paths.StateFile)
			h.s.St, h.s.Progress = st, nil
			plan, err := h.s.RepairPlan(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(plan) == 0 || plan[0].Kind != "resume" {
				t.Fatalf("repair plan %+v", plan)
			}
			for _, f := range plan {
				if f.Fix != nil && !f.Data {
					if err := f.Fix(context.Background()); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, ok := h.s.St.Instances["chat-a"]; ok || h.sys.users["sharkord-chat-a"] {
				t.Fatal("repair didn't finish the delete")
			}
		})
	}
}

// deleteSteps2 counts delete steps without needing a record.
func (s *Sys) deleteSteps2() []Step {
	s.St.Instances["x"] = &state.Instance{}
	defer delete(s.St.Instances, "x")
	return s.deleteSteps("x")
}

func TestRepairFindsMissingAndLeftovers(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	if plan, _ := h.s.RepairPlan(ctx); len(plan) != 0 {
		t.Fatalf("a healthy system has findings: %+v", plan)
	}
	// Break things.
	os.Remove(h.s.envPath("chat-a"))
	os.Remove(h.s.Caddy.SitePath("chat-a"))
	h.s.Firewall().Remove(ctx, firewall.Rule{Port: 40000, Proto: "udp", Instance: "chat-a"})
	h.sys.active["sharkord@chat-a.service"] = false
	// Leftovers from a server the records don't know.
	os.WriteFile(filepath.Join(h.s.Caddy.SitesDir, "ghost.caddy"), []byte("# Managed by shiver-server-manager (instance ghost). Do not edit.\n"), 0o644)
	os.WriteFile(filepath.Join(h.s.Caddy.SitesDir, "admins-own.caddy"), []byte("blog.example.com {}\n"), 0o644)
	h.sys.users["sharkord-ghost"] = true
	h.s.Firewall().Allow(ctx, firewall.Rule{Port: 40009, Proto: "udp", Instance: "ghost"})
	ghostData := filepath.Join(h.s.St.DefaultLocation(), "ghost")
	os.Mkdir(ghostData, 0o700)

	plan, err := h.s.RepairPlan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var whats []string
	for _, f := range plan {
		whats = append(whats, f.Kind+": "+f.What)
	}
	all := strings.Join(whats, "\n")
	for _, want := range []string{"redo step files", "redo step caddy-site", "redo step firewall", "start the server",
		"leftover: Caddy site file", "leftover: system user sharkord-ghost", "leftover: firewall rule 40009/udp (ghost)", "leftover: data folder"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "admins-own") {
		t.Fatal("an admin's own site file was called a leftover")
	}
	for _, f := range plan {
		if f.Fix != nil && !f.Data {
			if err := f.Fix(ctx); err != nil {
				t.Fatalf("%s: %v", f.What, err)
			}
		}
	}
	plan, _ = h.s.RepairPlan(ctx)
	if len(plan) != 1 || !plan[0].Data {
		t.Fatalf("after repair: %+v", plan)
	}
	if _, err := os.Stat(ghostData); err != nil {
		t.Fatal("data folder removed without asking")
	}
}

func TestRepairLeavesStoppedServersStopped(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	if err := h.s.Stop(ctx, "chat-a"); err != nil {
		t.Fatal(err)
	}
	if plan, _ := h.s.RepairPlan(ctx); len(plan) != 0 {
		t.Fatalf("plan for a deliberately stopped server: %+v", plan)
	}
}

// fileList lists every file and link under root, except the vault.
func fileList(root string) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if err == nil && !d.IsDir() && !strings.HasPrefix(rel, "vault/") {
			out = append(out, rel)
		}
		return nil
	})
	return out
}

func TestSwitchTLS(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.s.Create(ctx, req("chat-a")); err != nil {
		t.Fatal(err)
	}
	// Pretend Caddy issued a Let's Encrypt certificate earlier.
	leDir := filepath.Join(h.s.Caddy.DataDir, "certificates", "acme-v02.api.letsencrypt.org-directory", "chat-a.example.com")
	os.MkdirAll(leDir, 0o700)
	writeTestCert(t, filepath.Join(leDir, "chat-a.example.com.crt"))
	localDir := filepath.Join(h.s.Caddy.DataDir, "certificates", "local", "chat-a.example.com")

	// No self-signed certificate yet: the old one is kept.
	if err := h.s.SwitchTLS(ctx, "chat-a", state.TLSSelfSigned, false); err != nil {
		t.Fatal(err)
	}
	site, _ := os.ReadFile(h.s.Caddy.SitePath("chat-a"))
	if !strings.Contains(string(site), "issuer internal") || !strings.Contains(string(site), "sharkord-root.crt") {
		t.Fatalf("site file:\n%s", site)
	}
	if _, err := os.Stat(leDir); err != nil {
		t.Fatal("old certificate removed before the new one arrived")
	}
	// Once the new one exists, a repair removes the old.
	os.MkdirAll(localDir, 0o700)
	writeTestCert(t, filepath.Join(localDir, "chat-a.example.com.crt"))
	h.s.St.Instances["chat-a"].Operation = &state.Operation{Name: OpSwitch}
	if err := h.s.Resume(ctx, "chat-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leDir); err == nil {
		t.Fatal("old certificate kept")
	}
	if err := h.s.SwitchTLS(ctx, "chat-a", state.TLSSelfSigned, false); err == nil {
		t.Fatal("switching to the same type succeeded")
	}
	if err := h.s.SwitchTLS(ctx, "chat-a", state.TLSSelfSigned, true); err == nil {
		t.Fatal("test mode with self-signed accepted")
	}
}
