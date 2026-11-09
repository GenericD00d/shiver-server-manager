package ops

import (
	"context"
	"fmt"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/caddy"
	"github.com/GenericD00d/shiver-server-manager/internal/firewall"
	"github.com/GenericD00d/shiver-server-manager/internal/netinfo"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

// LetsEncryptCA is Let's Encrypt's production directory.
const LetsEncryptCA = "https://acme-v02.api.letsencrypt.org/directory"

// Firewall returns the backend chosen at setup.
func (s *Sys) Firewall() firewall.Firewall {
	if s.FirewallFor != nil {
		return s.FirewallFor(s.St.Settings.Firewall)
	}
	return firewall.For(s.St.Settings.Firewall, s.R)
}

// Issuer is the folder under Caddy's certificates/ for an instance.
func (s *Sys) Issuer(inst *state.Instance) string {
	switch {
	case inst.TLS == state.TLSSelfSigned:
		return "local"
	case inst.TestMode:
		return caddy.IssuerDir(caddy.StagingCA)
	case s.St.Settings.ACMECA != "":
		return caddy.IssuerDir(s.St.Settings.ACMECA)
	}
	return caddy.IssuerDir(LetsEncryptCA)
}

func (s *Sys) siteFile(id string) []byte {
	inst := s.St.Instances[id]
	return caddy.RenderSite(paths.Marker, caddy.Site{
		ID: id, Domain: inst.Domain, Port: inst.HTTPPort,
		SelfSigned: inst.TLS == state.TLSSelfSigned, TestMode: inst.TestMode,
	})
}

func (s *Sys) stepFirewall(id string) Step {
	inst := s.St.Instances[id]
	rules := firewall.InstanceRules(id, inst.WebRTCPort)
	return Step{
		Name: "firewall",
		Done: func(ctx context.Context) (bool, error) {
			have, err := s.Firewall().List(ctx)
			if err != nil {
				return false, err
			}
			if s.Firewall().Name() == "none" {
				return true, nil
			}
			for _, r := range rules {
				if !firewall.Contains(have, r) {
					return false, nil
				}
			}
			return true, nil
		},
		Do: func(ctx context.Context) error {
			for _, r := range rules {
				if err := s.Firewall().Allow(ctx, r); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func (s *Sys) stepSite(id string) Step {
	files := func() map[string][]byte { return map[string][]byte{s.Caddy.SitePath(id): s.siteFile(id)} }
	return Step{
		Name: "caddy-site",
		Done: func(context.Context) (bool, error) { return caddy.FilesMatch(files()), nil },
		Do:   func(ctx context.Context) error { return s.Caddy.Apply(ctx, files(), false) },
	}
}

// stepCertificate waits up to CertWait for the certificate. If it isn't
// there yet the step still finishes, since Caddy keeps trying; the status
// shows it as pending.
func (s *Sys) stepCertificate(id string) Step {
	waited := false
	return Step{
		Name: "certificate",
		Done: func(context.Context) (bool, error) {
			if waited {
				return true, nil
			}
			inst := s.St.Instances[id]
			c, err := s.Caddy.CertStatus(s.Issuer(inst), inst.Domain, s.Now())
			return c.Present, err
		},
		Do: func(ctx context.Context) error {
			defer func() { waited = true }()
			inst := s.St.Instances[id]
			if inst.TLS == state.TLSLetsEncrypt && s.St.Settings.ACMECA == "" && s.DNSCheck != nil {
				if r := s.DNSCheck(ctx, inst.Domain); !r.OK {
					s.emit(Event{Instance: id, Step: "certificate", Status: "note", Detail: r.Problem})
				}
			}
			deadline := s.Now().Add(s.CertWait)
			for {
				c, err := s.Caddy.CertStatus(s.Issuer(inst), inst.Domain, s.Now())
				if err != nil {
					return err
				}
				if c.Present {
					return nil
				}
				if !s.Now().Before(deadline) {
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(2 * time.Second):
				}
			}
			msg := fmt.Sprintf("The certificate for %s isn't there yet; Caddy keeps trying. Check with 'cert status %s'.", inst.Domain, id)
			if last, _ := s.Caddy.LastCertError(ctx, inst.Domain); last != "" {
				msg += " Last error: " + last
			}
			s.emit(Event{Instance: id, Step: "certificate", Status: "note", Detail: msg})
			return nil
		},
	}
}

// CertInfo is a certificate's status for display.
type CertInfo struct {
	Instance string `json:"instance"`
	caddy.Cert
	Pending   bool   `json:"pending"`
	LastError string `json:"lastError,omitempty"`
	Warning   string `json:"warning,omitempty"`
}

func (s *Sys) CertInfo(ctx context.Context, id string) (CertInfo, error) {
	inst, err := s.Instance(id)
	if err != nil {
		return CertInfo{}, err
	}
	c, err := s.Caddy.CertStatus(s.Issuer(inst), inst.Domain, s.Now())
	if err != nil {
		return CertInfo{}, err
	}
	ci := CertInfo{Instance: id, Cert: c, Pending: !c.Present}
	if ci.Pending {
		ci.LastError, _ = s.Caddy.LastCertError(ctx, inst.Domain)
	} else if c.RenewalFailing(s.Now()) {
		ci.Warning = "less than a sixth of this certificate's life is left, so Caddy's renewals are failing"
	}
	return ci, nil
}

// RetryCert rechecks DNS and reloads Caddy, which retries at once.
func (s *Sys) RetryCert(ctx context.Context, id string) (netinfo.DNSResult, error) {
	inst, err := s.Instance(id)
	if err != nil {
		return netinfo.DNSResult{}, err
	}
	var r netinfo.DNSResult
	if inst.TLS == state.TLSLetsEncrypt && s.St.Settings.ACMECA == "" && s.DNSCheck != nil {
		r = s.DNSCheck(ctx, inst.Domain)
		if !r.OK {
			return r, fmt.Errorf("%s", r.Problem)
		}
	}
	return r, s.Caddy.Reload(ctx)
}
