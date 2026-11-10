package ops

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GenericD00d/shiver-server-manager/internal/fsutil"
	"github.com/GenericD00d/shiver-server-manager/internal/osuser"
	"github.com/GenericD00d/shiver-server-manager/internal/plugins"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/systemd"
)

// ShiverStatus is an instance's installed Shiver next to the latest.
type ShiverStatus struct {
	Instance  string `json:"instance"`
	Installed string `json:"installed,omitempty"`
	Latest    string `json:"latest,omitempty"`
	// Switches: "on", "plugins off", "shiver off", or "" when unknown.
	Switches string `json:"switches,omitempty"`
}

func (s *Sys) ShiverStatus(ctx context.Context, id string, latest string) ShiverStatus {
	inst := s.St.Instances[id]
	st := ShiverStatus{Instance: id, Latest: latest}
	if m, ok := plugins.Installed(inst.DataPath); ok {
		st.Installed = m.Version
		st.Switches = s.pluginSwitches(ctx, inst.HTTPPort)
	}
	return st
}

// pluginSwitches asks Sharkord whether Shiver's client bundle is served:
// 200 when both switches are on, 403 while plugins are off, 404 while
// Shiver is.
func (s *Sys) pluginSwitches(ctx context.Context, port int) string {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/plugin-bundle/%s/client/index.js", port, plugins.ID), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return "on"
	case http.StatusForbidden:
		return "plugins off"
	case http.StatusNotFound:
		return "shiver off"
	}
	return ""
}

// InstallShiver installs or updates the Shiver plugin on one instance:
// download and check it, back up, swap the folder in while stopped, switch
// plugins on (first install, tested versions only), start, and confirm
// Sharkord logged it as loaded; otherwise the old copy goes back.
func (s *Sys) InstallShiver(ctx context.Context, id string) (string, error) {
	inst, err := s.requireUnlocked(ctx, id)
	if err != nil {
		return "", err
	}
	tested, isTested := sharkord.FindTested(inst.SharkordVersion)
	if !isTested {
		return "", fmt.Errorf("Sharkord %s isn't on the tested list, so its plugin SDK isn't known", inst.SharkordVersion)
	}
	rel, err := s.Shiver.Latest(ctx)
	if err != nil {
		return "", err
	}
	current, hadOld := plugins.Installed(inst.DataPath)
	if hadOld && current.Version == rel.PluginVersion() {
		return "", fmt.Errorf("%s already has Shiver %s, the latest", id, current.Version)
	}
	zb, err := s.Shiver.Download(ctx, rel)
	if err != nil {
		return "", err
	}
	staging, err := s.stagingDir(id, "plugin")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	root, m, err := plugins.Unpack(zb, staging)
	if err != nil {
		return "", err
	}
	if err := m.Check(tested.PluginSDK); err != nil {
		return "", err
	}
	return s.swapPlugin(ctx, id, inst, tested, staging, root, m)
}

// InstallPluginFile installs any plugin from a .zip or .tar(.gz/.zst) file
// on one instance, the same way as Shiver: check it, back up, swap it in,
// and put the previous copy back if Sharkord can't load it. It returns the
// plugin's manifest and a note for the admin.
func (s *Sys) InstallPluginFile(ctx context.Context, id, file string) (plugins.Manifest, string, error) {
	inst, err := s.requireUnlocked(ctx, id)
	if err != nil {
		return plugins.Manifest{}, "", err
	}
	tested, isTested := sharkord.FindTested(inst.SharkordVersion)
	if !isTested {
		return plugins.Manifest{}, "", fmt.Errorf("Sharkord %s isn't on the tested list, so its plugin SDK isn't known", inst.SharkordVersion)
	}
	staging, err := s.stagingDir(id, "plugin")
	if err != nil {
		return plugins.Manifest{}, "", err
	}
	defer os.RemoveAll(staging)
	root, m, err := plugins.UnpackFile(file, staging)
	if err != nil {
		return m, "", err
	}
	if err := m.Valid(tested.PluginSDK); err != nil {
		return m, "", err
	}
	note, err := s.swapPlugin(ctx, id, inst, tested, staging, root, m)
	return m, note, err
}

// Plugins lists the plugins installed on an instance.
func (s *Sys) Plugins(id string) ([]plugins.Manifest, error) {
	inst, err := s.Instance(id)
	if err != nil {
		return nil, err
	}
	return plugins.List(inst.DataPath), nil
}

// swapPlugin backs up, swaps the unpacked plugin in while the server is
// stopped, switches plugins on (first plugin, tested versions only),
// starts, and confirms Sharkord logged it as loaded; otherwise the old copy
// goes back.
func (s *Sys) swapPlugin(ctx context.Context, id string, inst *state.Instance, tested sharkord.TestedVersion, staging, root string, m plugins.Manifest) (string, error) {
	current, hadOld := plugins.InstalledPlugin(inst.DataPath, m.ID)
	name := m.Name
	if name == "" {
		name = m.ID
	}

	s.emit(Event{Instance: id, Step: "backup", Status: "run"})
	if _, err := s.Backup(ctx, id); err != nil {
		return "", err
	}
	unit := systemd.Unit(id)
	if err := s.Systemd.Stop(ctx, unit); err != nil {
		return "", err
	}
	uid, gid, err := s.UserIDs(osuser.InstanceUser(id))
	if err != nil {
		return "", err
	}
	pluginsDir := filepath.Join(inst.DataPath, "plugins")
	if err := os.MkdirAll(pluginsDir, 0o700); err != nil {
		return "", err
	}
	if err := s.Chown(pluginsDir, uid, gid); err != nil {
		return "", err
	}
	target := filepath.Join(pluginsDir, m.ID)
	old := filepath.Join(staging, "previous")
	if fsutil.Exists(target) {
		if err := os.Rename(target, old); err != nil {
			return "", err
		}
	}
	if err := os.Rename(root, target); err != nil {
		return "", err
	}
	if err := filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Owner only, keeping whether a file can run.
		mode := os.FileMode(0o600)
		if d.IsDir() {
			mode = 0o700
		} else if fi, err := d.Info(); err == nil && fi.Mode()&0o111 != 0 {
			mode = 0o700
		}
		if err := os.Chmod(p, mode); err != nil {
			return err
		}
		return s.Chown(p, uid, gid)
	}); err != nil {
		return "", err
	}

	note := ""
	if !hadOld {
		if tested.DBSwitches {
			if err := s.AsInstance(ctx, uid, gid, "internal-enable-plugins", sharkord.DBPath(inst.DataPath), m.ID); err != nil {
				note = fmt.Sprintf("couldn't switch plugins on (%v): in Sharkord, switch on plugins in the server settings, then %s under Settings -> Plugins", err, name)
			}
		} else {
			note = fmt.Sprintf("in Sharkord, switch on plugins in the server settings, then %s under Settings -> Plugins", name)
		}
	}

	since := s.Now().Add(-2 * time.Second)
	putBack := func(cause error) error {
		_ = s.Systemd.Stop(ctx, unit)
		_ = os.RemoveAll(target)
		if fsutil.Exists(old) {
			if err := os.Rename(old, target); err != nil {
				return fmt.Errorf("%v; and putting the previous %s back failed: %w", cause, name, err)
			}
		}
		_ = s.Systemd.Start(ctx, unit)
		if hadOld {
			return fmt.Errorf("%w; the previous %s (%s) is back", cause, name, current.Version)
		}
		return fmt.Errorf("%w; %s was removed again", cause, name)
	}
	if err := s.Systemd.Start(ctx, unit); err != nil {
		return "", putBack(err)
	}
	if err := s.WaitHealthy(ctx, inst.HTTPPort, 90*time.Second); err != nil {
		return "", putBack(err)
	}
	if err := s.waitPluginLoaded(ctx, unit, m.ID, m.Version, since); err != nil {
		return "", putBack(err)
	}
	return note, nil
}

// waitPluginLoaded reads the journal since the start for Sharkord's verdict
// on the plugin.
func (s *Sys) waitPluginLoaded(ctx context.Context, unit, pluginID, version string, since time.Time) error {
	if s.PluginLog == nil {
		return nil
	}
	loaded := "Plugin loaded: " + pluginID + "@v" + version
	failed := "Failed to load plugin " + pluginID + ":"
	deadline := time.Now().Add(30 * time.Second)
	for {
		out, err := s.PluginLog(ctx, unit, since)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, failed) {
				return fmt.Errorf("Sharkord couldn't load the plugin: %s", strings.TrimSpace(line[strings.Index(line, failed):]))
			}
			if strings.Contains(line, loaded) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			// Plugins switched off (on an untested version): nothing loads, and
			// that's not a failure of the plugin.
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// journalSince reads a unit's journal since a time, message text only.
func journalSince(r run.Runner) func(ctx context.Context, unit string, since time.Time) (string, error) {
	return func(ctx context.Context, unit string, since time.Time) (string, error) {
		res, err := r.Run(ctx, run.Cmd{Name: "journalctl", Args: []string{"-u", unit, "--since", since.Format("2006-01-02 15:04:05"), "-o", "cat", "--no-pager"}})
		return string(res.Stdout), err
	}
}
