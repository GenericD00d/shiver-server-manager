package ops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/GenericD00d/shiver-server-manager/internal/plugins"
	"github.com/GenericD00d/shiver-server-manager/internal/selfupdate"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
)

// CatalogPlugin is one Shiver plugin from plugins.json, with its latest
// release and what each server has.
type CatalogPlugin struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Repo        string `json:"repo"`
	Latest      string `json:"latest,omitempty"`
	// Error is why the latest release couldn't be looked up.
	Error   string          `json:"error,omitempty"`
	Servers []CatalogServer `json:"servers"`
}

// CatalogServer is one server's copy of a catalog plugin.
type CatalogServer struct {
	Instance  string `json:"instance"`
	Installed string `json:"installed,omitempty"`
	// Update: a newer release than the installed copy is out.
	Update bool `json:"update,omitempty"`
}

// PluginCatalog reads plugins.json and each plugin's latest release, next
// to what every server has installed. An unreadable plugin list is an
// error (wrapping plugins.ErrUnavailable); one plugin's release failing to
// load only marks that plugin.
func (s *Sys) PluginCatalog(ctx context.Context) ([]CatalogPlugin, error) {
	cat, err := s.Catalog.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	out := []CatalogPlugin{}
	for _, e := range cat.Plugins {
		p := CatalogPlugin{ID: e.ID, Name: e.Name, Description: e.Description, Repo: e.Repo, Servers: []CatalogServer{}}
		if rel, err := s.Catalog.Latest(ctx, e); err != nil {
			p.Error = err.Error()
		} else {
			p.Latest = rel.Version
		}
		for _, id := range s.IDs() {
			cs := CatalogServer{Instance: id}
			if m, ok := plugins.InstalledPlugin(s.St.Instances[id].DataPath, e.ID); ok {
				cs.Installed = m.Version
				cs.Update = p.Latest != "" && selfupdate.Newer(p.Latest, m.Version)
			}
			p.Servers = append(p.Servers, cs)
		}
		out = append(out, p)
	}
	return out, nil
}

// InstallFromCatalog installs or updates a plugin from plugins.json on one
// server: download its latest release and check its checksum, then the
// same steps as a plugin from a file (back up, swap in while stopped, put
// the previous copy back if Sharkord can't load it).
func (s *Sys) InstallFromCatalog(ctx context.Context, id, pluginID string) (plugins.Manifest, string, error) {
	inst, err := s.requireUnlocked(ctx, id)
	if err != nil {
		return plugins.Manifest{}, "", err
	}
	tested, isTested := sharkord.FindTested(inst.SharkordVersion)
	if !isTested {
		return plugins.Manifest{}, "", fmt.Errorf("Sharkord %s isn't on the tested list, so its plugin SDK isn't known", inst.SharkordVersion)
	}
	cat, err := s.Catalog.Catalog(ctx)
	if err != nil {
		return plugins.Manifest{}, "", err
	}
	e, ok := cat.Find(pluginID)
	if !ok {
		return plugins.Manifest{}, "", fmt.Errorf("there's no Shiver plugin called %q in the plugin list", pluginID)
	}
	rel, err := s.Catalog.Latest(ctx, e)
	if err != nil {
		return plugins.Manifest{}, "", err
	}
	if m, ok := plugins.InstalledPlugin(inst.DataPath, e.ID); ok && !selfupdate.Newer(rel.Version, m.Version) {
		return m, "", fmt.Errorf("%s already has %s %s, the latest", id, e.Name, m.Version)
	}
	staging, err := s.stagingDir(id, "plugin")
	if err != nil {
		return plugins.Manifest{}, "", err
	}
	defer os.RemoveAll(staging)
	archive := filepath.Join(staging, "download")
	if err := s.Catalog.Download(ctx, rel, archive); err != nil {
		return plugins.Manifest{}, "", err
	}
	unpacked := filepath.Join(staging, "unpacked")
	if err := os.Mkdir(unpacked, 0o700); err != nil {
		return plugins.Manifest{}, "", err
	}
	root, m, err := plugins.UnpackFile(archive, unpacked)
	if err != nil {
		return m, "", err
	}
	if m.ID != e.ID {
		return m, "", fmt.Errorf("%s's release holds the plugin %q, not %q", e.Name, m.ID, e.ID)
	}
	if err := m.Valid(tested.PluginSDK); err != nil {
		return m, "", err
	}
	note, err := s.swapPlugin(ctx, id, inst, tested, staging, root, m)
	return m, note, err
}
