package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
	"github.com/GenericD00d/shiver-server-manager/internal/validate"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// CreateRequest is what "Create instance" asks for; everything else has a
// default.
type CreateRequest struct {
	ID       string
	Domain   string
	TLS      state.TLS
	TestMode bool
	// SkipDNSCheck: the domain points at a proxy in front of the server.
	SkipDNSCheck bool
	Location     string // "" means the default location
	Encrypted    bool
	Version      string // "" means the newest tested version
}

// CheckCreate validates a request against the records and fills defaults.
func (s *Sys) CheckCreate(req *CreateRequest) error {
	if !s.Vault.Exists() {
		return errors.New("first-time setup hasn't finished (there's no vault yet); run setup first")
	}
	if err := validate.InstanceID(req.ID); err != nil {
		return err
	}
	if _, exists := s.St.Instances[req.ID]; exists {
		return fmt.Errorf("there's already a server called %q", req.ID)
	}
	d, err := validate.Domain(req.Domain)
	if err != nil {
		return err
	}
	req.Domain = d
	for id, inst := range s.St.Instances {
		if inst.Domain == d {
			return fmt.Errorf("server %q already uses %s", id, d)
		}
	}
	switch req.TLS {
	case "":
		req.TLS = state.TLSLetsEncrypt
	case state.TLSLetsEncrypt, state.TLSSelfSigned:
	default:
		return fmt.Errorf("certificate type must be letsencrypt or selfsigned, not %q", req.TLS)
	}
	if req.TestMode && req.TLS != state.TLSLetsEncrypt {
		return errors.New("test mode only applies to Let's Encrypt certificates")
	}
	if req.Version == "" {
		req.Version = sharkord.LatestTested().Version
	}
	if _, ok := sharkord.FindTested(req.Version); !ok {
		return fmt.Errorf("Sharkord %s isn't on the tested list; tested: %s", req.Version, testedList())
	}
	if req.Location == "" {
		req.Location = s.St.DefaultLocation()
	}
	req.Location = filepath.Clean(req.Location)
	known := false
	for _, l := range s.St.Settings.Locations {
		known = known || l.Path == req.Location
	}
	if !known {
		return fmt.Errorf("%s isn't a storage location; add it first", req.Location)
	}
	return s.checkRoom(req.Location, req.Encrypted, 0)
}

func testedList() string {
	var out string
	for i, t := range sharkord.Tested {
		if i > 0 {
			out += ", "
		}
		out += t.Version
	}
	return out
}

// Create adds the record with its ports reserved, then runs the create
// steps. On a failure the record keeps the operation for Retry or repair.
func (s *Sys) Create(ctx context.Context, req CreateRequest) error {
	if err := s.CheckCreate(&req); err != nil {
		return err
	}
	if s.St.Settings.PublicIPv4 == "" {
		ip, err := s.PublicIPv4(ctx)
		if err != nil {
			return err
		}
		s.St.Settings.PublicIPv4 = ip
	}
	usedHTTP, usedRTC := s.usedPorts()
	httpPort, err := s.pickPort(s.St.Settings.HTTPPorts, usedHTTP, false)
	if err != nil {
		return err
	}
	rtcPort, err := s.pickPort(s.St.Settings.WebRTCPorts, usedRTC, true)
	if err != nil {
		return err
	}
	s.St.Instances[req.ID] = &state.Instance{
		Status:          state.StatusCreating,
		Domain:          req.Domain,
		TLS:             req.TLS,
		TestMode:        req.TestMode,
		SharkordVersion: req.Version,
		Location:        req.Location,
		DataPath:        filepath.Join(req.Location, req.ID),
		HTTPPort:        httpPort,
		WebRTCPort:      rtcPort,
		Encrypted:       req.Encrypted,
		SkipDNSCheck:    req.SkipDNSCheck,
		CreatedAt:       s.Now().UTC(),
		Operation:       &state.Operation{Name: OpCreate, LastStep: "record", StartedAt: s.Now().UTC()},
	}
	if err := s.Save(); err != nil {
		return err
	}
	s.emit(Event{Instance: req.ID, Op: OpCreate, Step: "record", Status: "done",
		Detail: fmt.Sprintf("ports %d (web) and %d (voice)", httpPort, rtcPort)})
	return s.runSteps(ctx, req.ID, OpCreate, s.createSteps(req.ID))
}

// createSteps are the create steps after the record. They read everything
// from the record, so a resumed create rebuilds them exactly.
func (s *Sys) createSteps(id string) []Step {
	inst := s.St.Instances[id]
	steps := []Step{
		s.stepUser(id, inst),
		s.stepDownload(inst.SharkordVersion),
	}
	if inst.Encrypted {
		steps = append(steps, s.stepContainer(id), s.stepMount(id))
	}
	return append(steps,
		s.stepDataFolder(id, inst),
		s.stepFiles(id),
		s.stepFirstStart(id),
		s.stepService(id),
		s.stepFirewall(id),
		s.stepSite(id),
		s.stepCertificate(id),
		Step{
			Name: "active",
			Done: func(context.Context) (bool, error) {
				return s.St.Instances[id].Status == state.StatusActive, nil
			},
			Do: func(context.Context) error {
				s.St.Instances[id].Status = state.StatusActive
				return nil
			},
		},
	)
}

func (s *Sys) stepUser(id string, inst *state.Instance) Step {
	return Step{
		Name: "user",
		Done: func(ctx context.Context) (bool, error) {
			return osuser.UserExists(ctx, s.R, osuser.InstanceUser(id))
		},
		Do: func(ctx context.Context) error {
			return osuser.EnsureInstanceUser(ctx, s.R, id, inst.DataPath)
		},
	}
}

func (s *Sys) stepDownload(version string) Step {
	bin := sharkord.BinaryPath(s.Paths.OptDir, version)
	return Step{
		Name: "download",
		Done: func(context.Context) (bool, error) { return fsutil.Exists(bin), nil },
		Do: func(ctx context.Context) error {
			return s.Sharkord.Install(ctx, s.Paths.OptDir, version)
		},
	}
}

// stepDataFolder creates <location>/<id>, owned by the instance with mode 700.
func (s *Sys) stepDataFolder(id string, inst *state.Instance) Step {
	return Step{
		Name: "data-folder",
		Done: func(context.Context) (bool, error) {
			fi, err := os.Lstat(inst.DataPath)
			if errors.Is(err, os.ErrNotExist) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			if !fi.IsDir() {
				return false, fmt.Errorf("%s exists and isn't a folder", inst.DataPath)
			}
			uid, gid, err := s.UserIDs(osuser.InstanceUser(id))
			if err != nil {
				return false, err
			}
			ou, og, err := s.OwnerOf(inst.DataPath)
			modeOK := fi.Mode().Perm() == 0o700 || runtime.GOOS == "windows" // no modes on Windows dev builds
			return err == nil && ou == uid && og == gid && modeOK, err
		},
		Do: func(context.Context) error {
			if inst.Encrypted && !s.LUKS.IsMounted(context.Background(), inst.DataPath) {
				return errors.New("the encrypted container isn't mounted")
			}
			if _, err := os.Stat(inst.Location); err != nil {
				return fmt.Errorf("storage location %s: %w", inst.Location, err)
			}
			if err := os.Mkdir(inst.DataPath, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			uid, gid, err := s.UserIDs(osuser.InstanceUser(id))
			if err != nil {
				return err
			}
			if err := s.Chown(inst.DataPath, uid, gid); err != nil {
				return err
			}
			return os.Chmod(inst.DataPath, 0o700)
		},
	}
}

// renderFiles returns every generated file of an instance, path -> content.
func (s *Sys) renderFiles(id string) (map[string][]byte, error) {
	inst := s.St.Instances[id]
	var secrets map[string]string
	if old, err := os.ReadFile(s.envPath(id)); err == nil {
		secrets = sharkord.ReadSecretSettings(old)
	}
	env, err := s.renderEnvWithSecrets(id, secrets)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{
		s.envPath(id): env,
		filepath.Join(s.dropinDir(id), "data.conf"): systemd.DataDropin(paths.Marker, id, inst.Location, inst.DataPath),
	}
	if inst.Encrypted {
		files[filepath.Join(s.dropinDir(id), "encrypted.conf")] = systemd.EncryptedDropin(paths.Marker, id, inst.DataPath)
	}
	if inst.MemoryMaxMiB > 0 || inst.CPUPercent > 0 {
		files[filepath.Join(s.dropinDir(id), "limits.conf")] = systemd.LimitsDropin(paths.Marker, id, inst.MemoryMaxMiB, inst.CPUPercent)
	}
	return files, nil
}

func (s *Sys) filesInPlace(id string) (bool, error) {
	files, err := s.renderFiles(id)
	if err != nil {
		return false, err
	}
	for p, want := range files {
		if !fsutil.SameContent(p, want) {
			return false, nil
		}
	}
	inst := s.St.Instances[id]
	return fsutil.LinkPointsTo(s.linkPath(id), sharkord.BinaryPath(s.Paths.OptDir, inst.SharkordVersion)), nil
}

// writeFiles writes the env file, drop-ins and version link, then reloads
// systemd.
func (s *Sys) writeFiles(ctx context.Context, id string) error {
	files, err := s.renderFiles(id)
	if err != nil {
		return err
	}
	for p, data := range files {
		if err := fsutil.MkdirAllMode(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if filepath.Ext(p) == ".env" {
			mode = 0o600
		}
		if err := fsutil.WriteFileAtomic(p, data, mode, nil); err != nil {
			return err
		}
	}
	inst := s.St.Instances[id]
	if inst.MemoryMaxMiB == 0 && inst.CPUPercent == 0 {
		if err := fsutil.RemoveIfExists(filepath.Join(s.dropinDir(id), "limits.conf")); err != nil {
			return err
		}
	}
	if err := fsutil.MkdirAllMode(s.Paths.VersionLinkDir, 0o755); err != nil {
		return err
	}
	if err := fsutil.SymlinkAtomic(sharkord.BinaryPath(s.Paths.OptDir, inst.SharkordVersion), s.linkPath(id)); err != nil {
		return err
	}
	return s.Systemd.DaemonReload(ctx)
}

func (s *Sys) stepFiles(id string) Step {
	return Step{
		Name: "files",
		Done: func(context.Context) (bool, error) { return s.filesInPlace(id) },
		Do:   func(ctx context.Context) error { return s.writeFiles(ctx, id) },
	}
}

// stepFirstStart runs Sharkord once outside systemd and saves its token.
func (s *Sys) stepFirstStart(id string) Step {
	inst := s.St.Instances[id]
	return Step{
		Name: "first-start",
		Done: func(context.Context) (bool, error) {
			if s.Vault.HasSecret(id, vault.OwnerToken) {
				return true, nil
			}
			if sharkord.HasSettings(inst.DataPath) {
				s.emit(Event{Instance: id, Op: OpCreate, Step: "first-start", Status: "note", Detail: sharkord.ErrNoToken.Error()})
				return true, nil
			}
			return false, nil
		},
		Do: func(ctx context.Context) error {
			envBytes, err := os.ReadFile(s.envPath(id))
			if err != nil {
				return err
			}
			uid, gid, err := s.UserIDs(osuser.InstanceUser(id))
			if err != nil {
				return err
			}
			tok, err := s.FirstStart(ctx, sharkord.FirstStartOpts{
				Binary:   sharkord.BinaryPath(s.Paths.OptDir, inst.SharkordVersion),
				DataPath: inst.DataPath,
				Env:      sharkord.ParseEnvFile(envBytes),
				UID:      uint32(uid),
				GID:      uint32(gid),
				HTTPPort: inst.HTTPPort,
			})
			if errors.Is(err, sharkord.ErrNoToken) {
				s.emit(Event{Instance: id, Op: OpCreate, Step: "first-start", Status: "note", Detail: err.Error()})
				return nil
			}
			if err != nil {
				return err
			}
			if err := sharkord.CheckToken(inst.DataPath, tok); err != nil {
				return fmt.Errorf("the captured token doesn't match the database: %w", err)
			}
			return s.Vault.PutSecret(id, vault.OwnerToken, []byte(tok))
		},
	}
}

func (s *Sys) stepService(id string) Step {
	inst := s.St.Instances[id]
	unit := systemd.Unit(id)
	return Step{
		Name: "service",
		Done: func(ctx context.Context) (bool, error) {
			st, err := s.Systemd.Status(ctx, unit)
			if err != nil || st.Active != "active" || st.Enabled != "enabled" {
				return false, err
			}
			return s.WaitHealthy(ctx, inst.HTTPPort, 3*time.Second) == nil, nil
		},
		Do: func(ctx context.Context) error {
			if err := s.Systemd.EnableNow(ctx, unit); err != nil {
				return err
			}
			return s.WaitHealthy(ctx, inst.HTTPPort, 90*time.Second)
		},
	}
}
