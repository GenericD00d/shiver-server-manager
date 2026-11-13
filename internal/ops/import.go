package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/GenericD00d/shiver-server-manager/internal/backup"
	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/vault"
)

// ImportSource is where an import's data comes from: an archive (with how
// to open it) or a Sharkord data folder.
type ImportSource struct {
	Archive  string
	Identity age.Identity
	Folder   string
	// Docker names the container the folder belongs to, so the final copy
	// can offer "docker stop".
	Docker string
}

// ImportHooks ask the admin things mid-import.
type ImportHooks struct {
	// StopSource is called when the source database is still in use; it
	// returns once the admin says it's stopped.
	StopSource func(inUseBy string) error
	// Token asks for the owner token; "" skips (save it later).
	Token func() (string, error)
}

// ImportPlan is what Import found in the source before changing anything.
type ImportPlan struct {
	DataVersion string            // Sharkord version that last ran the data
	RunVersion  string            // tested version the import will run
	Settings    map[string]string // settings to carry over
	Manifest    *backup.Manifest  // for archives
}

// PlanImport reads the source: its Sharkord version and settings.
func (s *Sys) PlanImport(ctx context.Context, src ImportSource) (ImportPlan, error) {
	var p ImportPlan
	switch {
	case src.Archive != "":
		m, err := backup.ReadManifest(src.Archive, src.Identity)
		if err != nil {
			return p, err
		}
		p.Manifest = &m
		p.DataVersion = m.SharkordVersion
		p.Settings = map[string]string{}
		for k, v := range m.Instance.Settings {
			if norm, err := sharkord.NormalizeSetting(k, v); err == nil && norm != "" {
				p.Settings[k] = norm
			}
		}
	case src.Folder != "":
		if err := checkSourceFolder(src.Folder); err != nil {
			return p, err
		}
		v, err := sharkord.DataVersion(src.Folder)
		if err != nil {
			return p, err
		}
		p.DataVersion = v
		p.Settings, err = sharkord.SettingsFromConfigINI(filepath.Join(src.Folder, "config.ini"))
		if err != nil {
			return p, err
		}
	default:
		return p, errors.New("nothing to import from")
	}
	latest, err := s.Sharkord.Latest(ctx)
	if err != nil {
		return p, err
	}
	if p.DataVersion != "" && sharkord.CompareVersions(p.DataVersion, latest) > 0 {
		return p, fmt.Errorf("the data was last run by Sharkord %s, newer than any published release (%s); an older Sharkord can't open its database", p.DataVersion, latest)
	}
	p.RunVersion = latest
	return p, nil
}

// checkSourceFolder walks a Sharkord data folder with Lstat and refuses
// links, devices and pipes.
func checkSourceFolder(dir string) error {
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("%s isn't an absolute path", dir)
	}
	// No links along the way to the folder itself.
	for p := dir; p != filepath.Dir(p); p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link; give the real path", p)
		}
	}
	if _, err := os.Lstat(filepath.Join(dir, "db.sqlite")); err != nil {
		return fmt.Errorf("%s isn't a Sharkord data folder (no db.sqlite)", dir)
	}
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		top, _, _ := strings.Cut(filepath.ToSlash(rel), "/")
		if d.IsDir() && (top == "interface" || top == "drizzle" || top == "mediasoup" || top == "tmp") && rel == top {
			return filepath.SkipDir
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("%s isn't a plain file (links, devices and pipes aren't imported)", p)
		}
		return nil
	})
}

// ImportRequest is the new instance's identity.
type ImportRequest struct {
	CreateRequest
	Source ImportSource
	Plan   ImportPlan
}

// Import creates an instance from a source. Like create, it's resumable;
// resuming needs the same source again (the archive's passphrase isn't
// stored), so it's resumed by running the same import again.
func (s *Sys) Import(ctx context.Context, r ImportRequest, hooks ImportHooks) error {
	s.importSource, s.importHooks = &r.Source, hooks
	if inst, ok := s.St.Instances[r.ID]; ok && inst.Operation != nil && inst.Operation.Name == OpImport {
		return s.runSteps(ctx, r.ID, OpImport, s.importSteps(r.ID))
	}
	r.Version = r.Plan.RunVersion
	if r.TLS == "" && r.Plan.Manifest != nil {
		r.TLS = state.TLS(r.Plan.Manifest.Instance.TLS)
	}
	if err := s.CheckCreate(ctx, &r.CreateRequest); err != nil {
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
	settings := r.Plan.Settings
	if len(settings) == 0 {
		settings = nil
	}
	s.St.Instances[r.ID] = &state.Instance{
		Status: state.StatusCreating, Domain: r.Domain, TLS: r.TLS, TestMode: r.TestMode,
		SharkordVersion: r.Version, Location: r.Location, DataPath: filepath.Join(r.Location, r.ID),
		HTTPPort: httpPort, WebRTCPort: rtcPort, Encrypted: r.Encrypted, Settings: settings, SkipDNSCheck: r.SkipDNSCheck,
		CreatedAt: s.Now().UTC(),
		Operation: &state.Operation{Name: OpImport, LastStep: "record", StartedAt: s.Now().UTC()},
	}
	if err := s.Save(); err != nil {
		return err
	}
	return s.runSteps(ctx, r.ID, OpImport, s.importSteps(r.ID))
}

// importSteps are create's steps with the data copied in instead of a
// first start, and the token asked for.
func (s *Sys) importSteps(id string) []Step {
	var out []Step
	for _, st := range s.createSteps(id) {
		switch st.Name {
		case "first-start":
			out = append(out, s.stepImportData(id), s.stepImportToken(id))
		default:
			out = append(out, st)
		}
	}
	return out
}

func (s *Sys) stepImportData(id string) Step {
	inst := s.St.Instances[id]
	return Step{
		Name: "import-data",
		Done: func(context.Context) (bool, error) {
			return fsutil.Exists(sharkord.DBPath(inst.DataPath)), nil
		},
		Do: func(ctx context.Context) error {
			src := s.importSource
			if src == nil {
				return errNoSource
			}
			uid, gid, err := s.UserIDs(osuser.InstanceUser(id))
			if err != nil {
				return err
			}
			if src.Archive != "" {
				staging, _, err := s.unpackInto(id, src.Archive, src.Identity, "import")
				if err != nil {
					return err
				}
				defer os.RemoveAll(staging)
				return moveEntries(staging, inst.DataPath)
			}
			if err := s.copyFolder(ctx, src.Folder, inst.DataPath, false); err != nil {
				return err
			}
			for {
				who, err := DBInUse(sharkord.DBPath(src.Folder))
				if err != nil {
					return err
				}
				if who == "" {
					break
				}
				if s.importHooks.StopSource == nil {
					return fmt.Errorf("the source server is still running (%s); stop it and run the import again", who)
				}
				if err := s.importHooks.StopSource(who); err != nil {
					return err
				}
			}
			if err := s.copyFolder(ctx, src.Folder, inst.DataPath, true); err != nil {
				return err
			}
			// Hand everything to the new instance's user (Docker volumes
			// usually belong to UID 1000).
			return filepath.WalkDir(inst.DataPath, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.Type()&fs.ModeSymlink != 0 {
					return os.Remove(p)
				}
				if err := s.Chown(p, uid, gid); err != nil {
					return err
				}
				// Owner only, keeping whether a file can run: plugins keep
				// programs (yt-dlp, ffmpeg) in their data folders.
				mode := os.FileMode(0o600)
				if d.IsDir() {
					mode = 0o700
				} else if fi, err := d.Info(); err == nil && fi.Mode()&0o111 != 0 {
					mode = 0o700
				}
				return os.Chmod(p, mode)
			})
		},
	}
}

// copyFolder runs rsync from a Sharkord data folder, skipping what Sharkord
// unpacks at every start. The final pass deletes what's gone from the source.
func (s *Sys) copyFolder(ctx context.Context, from, to string, final bool) error {
	args := []string{"-aHAX", "--numeric-ids", "--no-links", "--no-devices", "--no-specials",
		"--exclude=/interface", "--exclude=/drizzle", "--exclude=/mediasoup", "--exclude=/tmp", "--exclude=/lost+found"}
	if final {
		args = append(args, "--delete")
	}
	args = append(args, filepath.Clean(from)+"/", filepath.Clean(to)+"/")
	_, err := s.R.Run(ctx, run.Cmd{Name: "rsync", Args: args, Timeout: 6 * time.Hour})
	return err
}

func (s *Sys) stepImportToken(id string) Step {
	inst := s.St.Instances[id]
	asked := false
	return Step{
		Name: "token",
		Done: func(context.Context) (bool, error) { return asked || s.Vault.HasSecret(id, vault.OwnerToken), nil },
		Do: func(context.Context) error {
			defer func() { asked = true }()
			var tok string
			if src := s.importSource; src != nil && src.Archive != "" {
				if m, err := backup.ReadManifest(src.Archive, src.Identity); err == nil && m.OwnerToken != nil {
					tok = *m.OwnerToken
				}
			}
			for attempt := 0; attempt < 4; attempt++ {
				if tok == "" {
					if s.importHooks.Token == nil {
						break
					}
					t, err := s.importHooks.Token()
					if err != nil {
						return err
					}
					if t == "" {
						break
					}
					tok = t
				}
				if err := sharkord.CheckToken(inst.DataPath, tok); err != nil {
					s.emit(Event{Instance: id, Step: "token", Status: "note", Detail: err.Error()})
					tok = ""
					continue
				}
				return s.Vault.PutSecret(id, vault.OwnerToken, []byte(strings.ToLower(strings.TrimSpace(tok))))
			}
			s.emit(Event{Instance: id, Step: "token", Status: "note", Detail: "no secret token saved; add it later with 'token save " + id + "'"})
			return nil
		},
	}
}

// DockerVolume finds the host folder mounted at Sharkord's data path in a
// Docker container.
func DockerVolume(ctx context.Context, r run.Runner, container string) (string, error) {
	res, err := r.Run(ctx, run.Cmd{Name: "docker", Args: []string{"inspect", container, "--format", "{{json .Mounts}}"}})
	if err != nil {
		return "", err
	}
	var mounts []struct {
		Source      string `json:"Source"`
		Destination string `json:"Destination"`
	}
	if err := json.Unmarshal(res.Stdout, &mounts); err != nil {
		return "", fmt.Errorf("can't read docker inspect's answer: %w", err)
	}
	for _, m := range mounts {
		if m.Destination == "/home/bun/.config/sharkord" {
			return m.Source, nil
		}
	}
	return "", fmt.Errorf("container %s has nothing mounted at /home/bun/.config/sharkord", container)
}
