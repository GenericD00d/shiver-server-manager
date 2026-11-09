package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// IDs lists instance IDs in order.
func (s *Sys) IDs() []string {
	ids := make([]string, 0, len(s.St.Instances))
	for id := range s.St.Instances {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Info is an instance's record plus its live state.
type Info struct {
	ID string `json:"id"`
	*state.Instance
	Unit       string `json:"unit"`
	Active     string `json:"active"`
	Healthy    bool   `json:"healthy"`
	TokenSaved bool   `json:"tokenSaved"`
}

func (s *Sys) Info(ctx context.Context, id string) (Info, error) {
	inst, err := s.Instance(id)
	if err != nil {
		return Info{}, err
	}
	info := Info{ID: id, Instance: inst, Unit: systemd.Unit(id), TokenSaved: s.Vault.HasSecret(id, vault.OwnerToken)}
	if st, err := s.Systemd.Status(ctx, info.Unit); err == nil {
		info.Active = st.Active
	}
	if info.Active == "active" {
		info.Healthy = sharkord.Healthz(ctx, inst.HTTPPort) == nil
	}
	return info, nil
}

func (s *Sys) requireActive(id string) (*state.Instance, error) {
	inst, err := s.Instance(id)
	if err != nil {
		return nil, err
	}
	if inst.Operation != nil {
		return nil, fmt.Errorf("server %q has an unfinished %s; run 'repair' first", id, inst.Operation.Name)
	}
	return inst, nil
}

// Start starts an instance and waits for it to answer.
func (s *Sys) Start(ctx context.Context, id string) error {
	inst, err := s.requireActive(id)
	if err != nil {
		return err
	}
	if err := s.Systemd.Start(ctx, systemd.Unit(id)); err != nil {
		return err
	}
	return s.WaitHealthy(ctx, inst.HTTPPort, 90*time.Second)
}

func (s *Sys) Stop(ctx context.Context, id string) error {
	if _, err := s.requireActive(id); err != nil {
		return err
	}
	return s.Systemd.Stop(ctx, systemd.Unit(id))
}

func (s *Sys) Restart(ctx context.Context, id string) error {
	inst, err := s.requireActive(id)
	if err != nil {
		return err
	}
	if err := s.Systemd.Restart(ctx, systemd.Unit(id)); err != nil {
		return err
	}
	return s.WaitHealthy(ctx, inst.HTTPPort, 90*time.Second)
}

// Set changes env settings ("KEY=VALUE"; an empty value goes back to the
// default), rewrites the env file and restarts. If the instance doesn't come
// back, the old settings are put back.
func (s *Sys) Set(ctx context.Context, id string, pairs []string) error {
	inst, err := s.requireActive(id)
	if err != nil {
		return err
	}
	oldSettings := map[string]string{}
	for k, v := range inst.Settings {
		oldSettings[k] = v
	}
	oldEnv, err := os.ReadFile(s.envPath(id))
	if err != nil {
		return err
	}
	secrets := sharkord.ReadSecretSettings(oldEnv)
	next := map[string]string{}
	for k, v := range inst.Settings {
		next[k] = v
	}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return fmt.Errorf("%q isn't KEY=VALUE", p)
		}
		k = strings.ToUpper(strings.TrimSpace(k))
		if !strings.HasPrefix(k, "SHARKORD_") {
			k = "SHARKORD_" + k
		}
		norm, err := sharkord.NormalizeSetting(k, v)
		if err != nil {
			return err
		}
		def, _ := sharkord.FindSetting(k)
		target := next
		if def.Secret {
			target = secrets
		}
		if norm == "" {
			delete(target, k)
		} else {
			target[k] = norm
		}
	}
	inst.Settings = next
	if len(next) == 0 {
		inst.Settings = nil
	}
	env, err := s.renderEnvWithSecrets(id, secrets)
	if err != nil {
		inst.Settings = oldSettings
		return err
	}
	if err := fsutil.WriteFileAtomic(s.envPath(id), env, 0o600, nil); err != nil {
		inst.Settings = oldSettings
		return err
	}
	if err := s.Save(); err != nil {
		return err
	}
	if err := s.Restart(ctx, id); err != nil {
		inst.Settings = oldSettings
		_ = fsutil.WriteFileAtomic(s.envPath(id), oldEnv, 0o600, nil)
		_ = s.Save()
		if rerr := s.Restart(ctx, id); rerr != nil {
			return fmt.Errorf("the server didn't start with the new settings (%v), and didn't come back with the old ones either: %w", err, rerr)
		}
		return fmt.Errorf("the server didn't start with the new settings, so the old ones are back: %w", err)
	}
	return nil
}

func (s *Sys) renderEnvWithSecrets(id string, secrets map[string]string) ([]byte, error) {
	inst := s.St.Instances[id]
	return sharkord.RenderEnv(sharkord.EnvParams{
		ID: id, DataPath: inst.DataPath, HTTPPort: inst.HTTPPort, WebRTCPort: inst.WebRTCPort,
		AnnouncedIPv4: s.St.Settings.PublicIPv4, Settings: inst.Settings, SecretSettings: secrets,
		ManagedByMarker: paths.Marker,
	})
}

// SaveToken checks a token against the instance's database, then saves it.
func (s *Sys) SaveToken(id, token string) error {
	inst, err := s.Instance(id)
	if err != nil {
		return err
	}
	token = strings.ToLower(strings.TrimSpace(token))
	if err := sharkord.CheckToken(inst.DataPath, token); err != nil {
		return err
	}
	return s.Vault.PutSecret(id, vault.OwnerToken, []byte(token))
}

// Token reads a saved token. The caller re-checks the password first.
func (s *Sys) Token(k *vault.Key, id string) (string, error) {
	if _, err := s.Instance(id); err != nil {
		return "", err
	}
	b, err := s.Vault.GetSecret(k, id, vault.OwnerToken)
	if errors.Is(err, vault.ErrNoSecret) {
		return "", fmt.Errorf("no secret token saved for %q; use 'token save %s' if you have it", id, id)
	}
	if err != nil {
		return "", err
	}
	t := string(b)
	clear(b)
	return t, nil
}

// Logs returns the journal plus the tail of Sharkord's error.log.
func (s *Sys) Logs(ctx context.Context, id string, lines int) (string, error) {
	if _, err := s.Instance(id); err != nil {
		return "", err
	}
	return s.Systemd.Logs(ctx, systemd.Unit(id), lines)
}
