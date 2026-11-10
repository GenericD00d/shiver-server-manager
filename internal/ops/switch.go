package ops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

// SwitchTLS changes an instance's certificate type (or Let's Encrypt test
// mode): new site file, wait for the new certificate, then delete the
// domain's certificate folders under every other issuer. It never falls
// back to self-signed by itself.
func (s *Sys) SwitchTLS(ctx context.Context, id string, tls state.TLS, testMode bool) error {
	inst, err := s.requireActive(id)
	if err != nil {
		return err
	}
	if tls != state.TLSLetsEncrypt && tls != state.TLSSelfSigned {
		return fmt.Errorf("certificate type must be letsencrypt or selfsigned, not %q", tls)
	}
	if testMode && tls != state.TLSLetsEncrypt {
		return errors.New("test mode only applies to Let's Encrypt certificates")
	}
	if inst.TLS == tls && inst.TestMode == testMode {
		return fmt.Errorf("%s already uses that certificate type", id)
	}
	inst.TLS, inst.TestMode = tls, testMode
	inst.Operation = &state.Operation{Name: OpSwitch, StartedAt: s.Now().UTC()}
	if err := s.Save(); err != nil {
		return err
	}
	return s.runSteps(ctx, id, OpSwitch, s.switchSteps(id))
}

func (s *Sys) switchSteps(id string) []Step {
	return []Step{s.stepSite(id), s.stepCertificate(id), s.stepOldCerts(id)}
}

// stepOldCerts deletes the domain's certificates under other issuers, but
// only once the current one exists, since Caddy may still be serving an old
// one. Until then there's nothing to do; repair finishes it later.
func (s *Sys) stepOldCerts(id string) Step {
	inst := s.St.Instances[id]
	newPresent := func() bool {
		c, _ := s.Caddy.CertStatus(s.Issuer(inst), inst.Domain, s.Now())
		return c.Present
	}
	return Step{
		Name: "old-certificates",
		Done: func(context.Context) (bool, error) {
			if !newPresent() {
				return true, nil
			}
			for _, iss := range s.otherIssuers(inst) {
				if c, _ := s.Caddy.CertStatus(iss, inst.Domain, s.Now()); c.Present {
					return false, nil
				}
			}
			return true, nil
		},
		Do: func(context.Context) error {
			var errs []error
			for _, iss := range s.otherIssuers(inst) {
				errs = append(errs, s.Caddy.DeleteCert(iss, inst.Domain))
			}
			return errors.Join(errs...)
		},
	}
}

func (s *Sys) otherIssuers(inst *state.Instance) []string {
	cur := s.Issuer(inst)
	var out []string
	for _, iss := range s.issuersFor(inst) {
		if iss != cur && !slices.Contains(out, iss) {
			out = append(out, iss)
		}
	}
	return out
}

// SetDNSCheck turns the "does the domain point here?" check on or off for a
// server, for a domain whose A record holds a proxy's address.
func (s *Sys) SetDNSCheck(id string, on bool) error {
	inst, err := s.Instance(id)
	if err != nil {
		return err
	}
	inst.SkipDNSCheck = !on
	return s.Save()
}

// RootInfo describes the self-signed root members install.
type RootInfo struct {
	PEM         []byte    `json:"-"`
	Fingerprint string    `json:"sha256Fingerprint"`
	NotAfter    time.Time `json:"notAfter"`
	URLs        []string  `json:"downloadURLs"`
	Warning     string    `json:"warning,omitempty"`
}

// RootRefuseLeft is when Caddy refuses to start with this configuration:
// its intermediate (730 days) must fit inside the root's remaining life.
const RootRefuseLeft = 2 * 365 * 24 * time.Hour

// RootWarnLeft is when to start warning: 90 days before Caddy refuses.
const RootWarnLeft = RootRefuseLeft + 90*24*time.Hour

func (s *Sys) RootInfo() (RootInfo, error) {
	pem, fp, notAfter, err := s.Caddy.RootCert()
	if err != nil {
		return RootInfo{}, fmt.Errorf("there's no self-signed root yet; Caddy makes it with the first self-signed server (%w)", err)
	}
	r := RootInfo{PEM: pem, Fingerprint: fp, NotAfter: notAfter}
	for _, id := range s.IDs() {
		if inst := s.St.Instances[id]; inst.TLS == state.TLSSelfSigned {
			r.URLs = append(r.URLs, "http://"+inst.Domain+"/sharkord-root.crt")
		}
	}
	if left := notAfter.Sub(s.Now()); left < RootWarnLeft {
		r.Warning = fmt.Sprintf("the root expires on %s. Caddy can't renew it, and refuses to start once it has under 2 years left. Plan a new root (members will need to install it again)", notAfter.Format("2006-01-02"))
	}
	return r, nil
}
