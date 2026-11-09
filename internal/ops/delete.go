package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GenericD00d/shiver-server-manager/internal/caddy"
	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
)

// Delete removes an instance and all its data, in the checklist's order.
// Every step succeeds when its target is already gone, so an interrupted
// delete can just be run again. The caller has already checked the
// password and had the ID typed back.
func (s *Sys) Delete(ctx context.Context, id string) error {
	inst, err := s.Instance(id)
	if err != nil {
		return err
	}
	if inst.Operation != nil && inst.Operation.Name != OpDelete && inst.Operation.Name != OpCreate {
		return fmt.Errorf("server %q has an unfinished %s; run 'repair' first", id, inst.Operation.Name)
	}
	inst.Status = state.StatusDeleting
	inst.Operation = &state.Operation{Name: OpDelete, StartedAt: s.Now().UTC()}
	if err := s.Save(); err != nil {
		return err
	}
	return s.runSteps(ctx, id, OpDelete, s.deleteSteps(id))
}

// safeDataPath refuses to delete anything but <location>/<id>.
func safeDataPath(inst *state.Instance, id string) (string, error) {
	want := filepath.Join(inst.Location, id)
	if filepath.Clean(inst.DataPath) != want || inst.Location == "" || !filepath.IsAbs(want) {
		return "", fmt.Errorf("refusing to delete %q: it isn't %s", inst.DataPath, want)
	}
	return want, nil
}

func (s *Sys) deleteSteps(id string) []Step {
	inst := s.St.Instances[id]
	unit := systemd.Unit(id)
	gone := func(p string) func(context.Context) (bool, error) {
		return func(context.Context) (bool, error) { return !fsutil.Exists(p), nil }
	}
	return []Step{
		{
			// First, or systemd would restart a half-deleted instance and
			// Sharkord would set up a fresh, empty one in its place.
			Name: "service",
			Done: func(ctx context.Context) (bool, error) {
				st, err := s.Systemd.Status(ctx, unit)
				if err != nil {
					return false, err
				}
				return st.Active != "active" && st.Active != "activating" && st.Enabled != "enabled", nil
			},
			Do: func(ctx context.Context) error { return s.Systemd.DisableNow(ctx, unit) },
		},
		{
			Name: "caddy-site",
			Done: gone(s.Caddy.SitePath(id)),
			Do: func(ctx context.Context) error {
				return s.Caddy.Apply(ctx, map[string][]byte{s.Caddy.SitePath(id): nil}, false)
			},
		},
		{
			Name: "certificate",
			Done: func(context.Context) (bool, error) {
				for _, iss := range s.issuersFor(inst) {
					c, _ := s.Caddy.CertStatus(iss, inst.Domain, s.Now())
					if c.Present {
						return false, nil
					}
				}
				return true, nil
			},
			Do: func(context.Context) error {
				var errs []error
				for _, iss := range s.issuersFor(inst) {
					errs = append(errs, s.Caddy.DeleteCert(iss, inst.Domain))
				}
				return errors.Join(errs...)
			},
		},
		{
			Name: "firewall",
			Done: func(ctx context.Context) (bool, error) {
				rules, err := s.Firewall().List(ctx)
				for _, r := range rules {
					if r.Instance == id {
						return false, err
					}
				}
				return true, err
			},
			Do: func(ctx context.Context) error {
				rules, err := s.Firewall().List(ctx)
				if err != nil {
					return err
				}
				for _, r := range rules {
					if r.Instance == id {
						if err := s.Firewall().Remove(ctx, r); err != nil {
							return err
						}
					}
				}
				return nil
			},
		},
		{
			Name: "ports",
			Done: func(context.Context) (bool, error) { return inst.HTTPPort == 0 && inst.WebRTCPort == 0, nil },
			Do: func(context.Context) error {
				inst.HTTPPort, inst.WebRTCPort = 0, 0
				return nil
			},
		},
		{
			Name: "data-folder",
			Done: func(context.Context) (bool, error) {
				p, err := safeDataPath(inst, id)
				if err != nil {
					return false, err
				}
				return !fsutil.Exists(p), nil
			},
			Do: func(ctx context.Context) error {
				p, err := safeDataPath(inst, id)
				if err != nil {
					return err
				}
				if inst.Encrypted && s.EraseContainer != nil {
					if err := s.EraseContainer(ctx, id); err != nil {
						return err
					}
				}
				return os.RemoveAll(p)
			},
		},
		{
			Name: "files",
			Done: func(context.Context) (bool, error) {
				return !fsutil.Exists(s.envPath(id)) && !fsutil.Exists(s.linkPath(id)) &&
					!fsutil.Exists(s.dropinDir(id)) && !s.Vault.HasAnySecret(id), nil
			},
			Do: func(ctx context.Context) error {
				for _, p := range []string{s.envPath(id), s.linkPath(id)} {
					if err := fsutil.RemoveIfExists(p); err != nil {
						return err
					}
				}
				if err := os.RemoveAll(s.dropinDir(id)); err != nil {
					return err
				}
				if err := s.Vault.DeleteSecrets(id); err != nil {
					return err
				}
				return s.Systemd.DaemonReload(ctx)
			},
		},
		{
			Name: "user",
			Done: func(ctx context.Context) (bool, error) {
				ok, err := osuser.UserExists(ctx, s.R, osuser.InstanceUser(id))
				return !ok, err
			},
			Do: func(ctx context.Context) error { return osuser.DeleteInstanceUser(ctx, s.R, id) },
		},
		{
			Name: "record",
			Done: func(context.Context) (bool, error) {
				_, ok := s.St.Instances[id]
				return !ok, nil
			},
			Do: func(context.Context) error {
				delete(s.St.Instances, id)
				return s.Save()
			},
		},
	}
}

// issuersFor lists every issuer folder a domain's certificate could be in,
// so switching types or test mode never leaves one behind.
func (s *Sys) issuersFor(inst *state.Instance) []string {
	out := []string{"local", caddy.IssuerDir(caddy.StagingCA), caddy.IssuerDir(LetsEncryptCA)}
	if s.St.Settings.ACMECA != "" {
		out = append(out, caddy.IssuerDir(s.St.Settings.ACMECA))
	}
	return out
}

// ruleIDs maps firewall rules to the instances they belong to.
func ruleIDs(rules []firewall.Rule) map[string]bool {
	m := map[string]bool{}
	for _, r := range rules {
		m[r.Instance] = true
	}
	return m
}

func isManaged(p, marker string) bool {
	b, err := os.ReadFile(p)
	return err == nil && strings.Contains(string(b), marker)
}
