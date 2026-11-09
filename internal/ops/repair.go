package ops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/validate"
)

// Finding is one difference between the records and the system.
type Finding struct {
	Kind     string `json:"kind"` // "resume", "missing", "leftover", "problem"
	Instance string `json:"instance,omitempty"`
	What     string `json:"what"`
	// Ask means the fix removes something, so the admin is asked first.
	Ask bool `json:"ask"`
	// Data means the fix deletes community data; it's never done without an
	// explicit yes, even with --yes.
	Data bool                        `json:"data"`
	Fix  func(context.Context) error `json:"-"`
}

// RepairPlan compares what should exist (the records) with what does.
// The records are never changed to match the system.
func (s *Sys) RepairPlan(ctx context.Context) ([]Finding, error) {
	var out []Finding
	for _, id := range s.IDs() {
		inst := s.St.Instances[id]
		if inst.Operation != nil {
			id := id
			out = append(out, Finding{Kind: "resume", Instance: id,
				What: fmt.Sprintf("finish the unfinished %s (stopped after %q)", inst.Operation.Name, inst.Operation.LastStep),
				Fix:  func(ctx context.Context) error { return s.Resume(ctx, id) }})
			continue
		}
		f, err := s.missingFor(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, f...)
	}
	left, err := s.leftovers(ctx)
	if err != nil {
		return nil, err
	}
	out = append(out, left...)

	// Caddy's own ports.
	have, err := s.Firewall().List(ctx)
	if err != nil {
		return nil, err
	}
	if s.Firewall().Name() != "none" {
		for _, r := range firewall.CaddyRules {
			if !firewall.Contains(have, r) {
				r := r
				out = append(out, Finding{Kind: "missing", What: "firewall rule " + r.String(),
					Fix: func(ctx context.Context) error { return s.Firewall().Allow(ctx, r) }})
			}
		}
	}
	return out, nil
}

// missingFor reruns an active instance's create checks. A missing data
// folder or database is reported, never recreated: that would start a
// fresh, empty community in its place.
func (s *Sys) missingFor(ctx context.Context, id string) ([]Finding, error) {
	inst := s.St.Instances[id]
	var out []Finding
	for _, st := range s.createSteps(id) {
		switch st.Name {
		case "certificate", "active":
			continue
		case "first-start":
			continue
		case "data-folder":
			if !fsutil.Exists(inst.DataPath) {
				out = append(out, Finding{Kind: "problem", Instance: id,
					What: fmt.Sprintf("the data folder %s is missing; is its drive (or encrypted container) attached? Nothing was changed", inst.DataPath)})
				return out, nil
			}
			continue
		case "service":
			if inst.Stopped {
				continue
			}
		}
		done, err := st.Done(ctx)
		if err != nil {
			out = append(out, Finding{Kind: "problem", Instance: id, What: fmt.Sprintf("%s: %v", st.Name, err)})
			continue
		}
		if !done {
			st := st
			what := "redo step " + st.Name
			if st.Name == "service" {
				what = "start the server, which stopped unexpectedly"
			}
			out = append(out, Finding{Kind: "missing", Instance: id, What: what, Fix: st.Do})
		}
	}
	return out, nil
}

func (s *Sys) known(id string) bool {
	_, ok := s.St.Instances[id]
	return ok
}

// leftovers finds things carrying the manager's marker or naming pattern
// that belong to no instance.
func (s *Sys) leftovers(ctx context.Context) ([]Finding, error) {
	var out []Finding
	add := func(id, what string, data bool, fix func(context.Context) error) {
		out = append(out, Finding{Kind: "leftover", Instance: id, What: what, Ask: true, Data: data, Fix: fix})
	}

	// Site files (including a change-domain's .next file).
	if entries, err := os.ReadDir(s.Caddy.SitesDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			id := strings.TrimSuffix(strings.TrimSuffix(name, ".caddy"), ".next")
			p := filepath.Join(s.Caddy.SitesDir, name)
			if !strings.HasSuffix(name, ".caddy") || !isManaged(p, paths.Marker) {
				continue
			}
			if s.known(id) && !strings.HasSuffix(name, ".next.caddy") {
				continue
			}
			if s.known(id) && s.St.Instances[id].Operation != nil {
				continue
			}
			add(id, "Caddy site file "+p, false, func(ctx context.Context) error {
				return s.Caddy.Apply(ctx, map[string][]byte{p: nil}, false)
			})
		}
	}
	// Env files and version links.
	for _, dir := range []struct{ path, suffix, what string }{
		{s.Paths.EnvDir, ".env", "env file"},
		{s.Paths.VersionLinkDir, "", "version link"},
	} {
		entries, _ := os.ReadDir(dir.path)
		for _, e := range entries {
			id := strings.TrimSuffix(e.Name(), dir.suffix)
			if validate.InstanceID(id) != nil || s.known(id) || (dir.suffix != "" && !strings.HasSuffix(e.Name(), dir.suffix)) {
				continue
			}
			p := filepath.Join(dir.path, e.Name())
			add(id, dir.what+" "+p, false, func(context.Context) error { return fsutil.RemoveIfExists(p) })
		}
	}
	// Unit drop-ins.
	if entries, err := os.ReadDir(s.Paths.SystemdDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "sharkord@") || !strings.HasSuffix(name, ".service.d") {
				continue
			}
			id := strings.TrimSuffix(strings.TrimPrefix(name, "sharkord@"), ".service.d")
			p := filepath.Join(s.Paths.SystemdDir, name)
			if s.known(id) || !isManaged(filepath.Join(p, "data.conf"), paths.Marker) {
				continue
			}
			add(id, "unit drop-ins "+p, false, func(ctx context.Context) error {
				if err := os.RemoveAll(p); err != nil {
					return err
				}
				return s.Systemd.DaemonReload(ctx)
			})
		}
	}
	// System users.
	if res, err := s.R.Run(ctx, run.Cmd{Name: "getent", Args: []string{"passwd"}}); err == nil {
		for _, line := range strings.Split(string(res.Stdout), "\n") {
			name, _, _ := strings.Cut(line, ":")
			id, ok := strings.CutPrefix(name, paths.InstanceUserPrefix)
			if !ok || validate.InstanceID(id) != nil || s.known(id) {
				continue
			}
			add(id, "system user "+name, false, func(ctx context.Context) error { return osuser.DeleteInstanceUser(ctx, s.R, id) })
		}
	}
	// Firewall rules.
	rules, err := s.Firewall().List(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if r.Instance == firewall.Caddy || r.Instance == firewall.SSH || s.known(r.Instance) {
			continue
		}
		r := r
		add(r.Instance, "firewall rule "+r.String(), false, func(ctx context.Context) error { return s.Firewall().Remove(ctx, r) })
	}
	// Vault secrets.
	for _, id := range s.Vault.SecretIDs() {
		if !s.known(id) {
			id := id
			add(id, "secrets in the vault (secret token, keys)", false, func(context.Context) error { return s.Vault.DeleteSecrets(id) })
		}
	}
	// Data folders and containers in storage locations. Never removed
	// without asking.
	for _, loc := range s.St.Settings.Locations {
		entries, _ := os.ReadDir(loc.Path)
		for _, e := range entries {
			name := e.Name()
			id := strings.TrimSuffix(name, ".luks")
			if validate.InstanceID(id) != nil || s.known(id) {
				continue
			}
			p := filepath.Join(loc.Path, name)
			what := "data folder " + p
			if strings.HasSuffix(name, ".luks") {
				what = "encrypted container " + p
			} else if !e.IsDir() {
				continue
			}
			add(id, what+" (deleting it erases that community for good)", true, func(context.Context) error { return os.RemoveAll(p) })
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out, nil
}

// Stopped marks an instance as stopped (or not) by an admin.
func (s *Sys) setStopped(id string, stopped bool) error {
	inst, err := s.Instance(id)
	if err != nil {
		return err
	}
	if inst.Stopped == stopped {
		return nil
	}
	inst.Stopped = stopped
	return s.Save()
}
