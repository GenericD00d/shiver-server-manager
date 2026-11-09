package ops

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/caddy"
	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/validate"
)

// ChangeDomain moves an instance to a new domain: the new domain's site
// file goes in beside the old one (both work for a moment), the new
// certificate is waited for, then the new file replaces the old and the
// old domain's certificates go. Ports, firewall rules and the instance stay
// as they are; members need the new address.
func (s *Sys) ChangeDomain(ctx context.Context, id, domain string, force bool) error {
	inst, err := s.requireActive(id)
	if err != nil {
		return err
	}
	d, err := validate.Domain(domain)
	if err != nil {
		return err
	}
	if d == inst.Domain {
		return fmt.Errorf("%s already uses %s", id, d)
	}
	for other, i := range s.St.Instances {
		if i.Domain == d {
			return fmt.Errorf("server %q already uses %s", other, d)
		}
	}
	if s.wantDNSCheck(inst) && !force {
		if r := s.DNSCheck(ctx, d); !r.OK {
			return fmt.Errorf("%s (use --force to change anyway; the certificate will wait until DNS is right)", r.Problem)
		}
	}
	inst.Operation = &state.Operation{Name: OpDomain, StartedAt: s.Now().UTC(),
		Params: map[string]string{"old": inst.Domain, "new": d}}
	if err := s.Save(); err != nil {
		return err
	}
	return s.runSteps(ctx, id, OpDomain, s.domainSteps(id))
}

func (s *Sys) nextSitePath(id string) string {
	return filepath.Join(s.Caddy.SitesDir, id+".next.caddy")
}

func (s *Sys) domainSteps(id string) []Step {
	inst := s.St.Instances[id]
	op := inst.Operation
	oldD, newD := op.Params["old"], op.Params["new"]
	render := func(d string) []byte {
		return caddy.RenderSite(paths.Marker, caddy.Site{ID: id, Domain: d, Port: inst.HTTPPort,
			SelfSigned: inst.TLS == state.TLSSelfSigned, TestMode: inst.TestMode})
	}
	certFor := func(d string) bool {
		c, _ := s.Caddy.CertStatus(s.Issuer(inst), d, s.Now())
		return c.Present
	}
	waited := false
	return []Step{
		{
			Name: "new-site",
			Done: func(context.Context) (bool, error) {
				return op.Params["switched"] == "yes" || fsutil.SameContent(s.nextSitePath(id), render(newD)), nil
			},
			Do: func(ctx context.Context) error {
				return s.Caddy.Apply(ctx, map[string][]byte{s.nextSitePath(id): render(newD)}, false)
			},
		},
		{
			Name: "new-certificate",
			Done: func(context.Context) (bool, error) {
				return waited || op.Params["switched"] == "yes" || certFor(newD), nil
			},
			Do: func(ctx context.Context) error {
				defer func() { waited = true }()
				deadline := s.Now().Add(s.CertWait)
				for !certFor(newD) && s.Now().Before(deadline) {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(2 * time.Second):
					}
				}
				if !certFor(newD) {
					s.emit(Event{Instance: id, Step: "new-certificate", Status: "note",
						Detail: fmt.Sprintf("the certificate for %s isn't there yet; Caddy keeps trying", newD)})
				}
				return nil
			},
		},
		{
			Name: "switch",
			Done: func(context.Context) (bool, error) { return op.Params["switched"] == "yes", nil },
			Do: func(ctx context.Context) error {
				// One validated change: the new file replaces both.
				if err := s.Caddy.Apply(ctx, map[string][]byte{
					s.Caddy.SitePath(id): render(newD),
					s.nextSitePath(id):   nil,
				}, false); err != nil {
					return err
				}
				inst.Domain = newD
				op.Params["switched"] = "yes"
				return nil
			},
		},
		{
			Name: "old-certificates",
			Done: func(context.Context) (bool, error) {
				for _, iss := range s.issuersFor(inst) {
					if c, _ := s.Caddy.CertStatus(iss, oldD, s.Now()); c.Present {
						return false, nil
					}
				}
				return true, nil
			},
			Do: func(context.Context) error {
				var errs []error
				for _, iss := range s.issuersFor(inst) {
					errs = append(errs, s.Caddy.DeleteCert(iss, oldD))
				}
				return errors.Join(errs...)
			},
		},
	}
}

// DomainWarnings lists settings that still name the old domain.
func DomainWarnings(inst *state.Instance, oldDomain string) []string {
	var out []string
	for k, v := range inst.Settings {
		if strings.Contains(v, oldDomain) {
			out = append(out, k)
		}
	}
	return out
}

// SetLimits caps an instance's memory (MiB) and CPU (percent of one core);
// 0 removes that cap. MemoryHigh is set at 90% of MemoryMax so the instance
// slows down before it's killed and restarted.
func (s *Sys) SetLimits(ctx context.Context, id string, memoryMiB, cpuPercent int) error {
	inst, err := s.requireActive(id)
	if err != nil {
		return err
	}
	if memoryMiB < 0 || cpuPercent < 0 || (memoryMiB > 0 && memoryMiB < 256) {
		return errors.New("give memory in MiB (at least 256) and CPU as a percentage of one core (200 is two cores); 0 removes a cap")
	}
	inst.MemoryMaxMiB, inst.CPUPercent = memoryMiB, cpuPercent
	if err := s.writeFiles(ctx, id); err != nil {
		return err
	}
	if err := s.Save(); err != nil {
		return err
	}
	if inst.Stopped {
		return nil
	}
	return s.Restart(ctx, id)
}

// RemoveEverything deletes every instance, for uninstalling. The vault,
// backups and the manager's records stay, so a reinstall can restore.
func (s *Sys) RemoveEverything(ctx context.Context) error {
	var errs []error
	for _, id := range s.IDs() {
		if err := s.Delete(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}
	for _, t := range []string{"shiver-server-manager-health.timer", "shiver-server-manager-backup.timer"} {
		_ = s.Systemd.DisableNow(ctx, t)
	}
	return errors.Join(errs...)
}
