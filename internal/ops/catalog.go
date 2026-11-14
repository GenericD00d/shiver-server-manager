package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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

// InstallFromCatalog installs or updates plugins from plugins.json on one
// server: download each one's latest release and check its checksum, then
// the same steps as a plugin from a file, once for the lot (back up, swap
// in while stopped, put the previous copies back if Sharkord can't load
// one). Plugins already at their latest are skipped; it's an error only
// when nothing is left to install.
func (s *Sys) InstallFromCatalog(ctx context.Context, id string, pluginIDs ...string) ([]plugins.Manifest, []string, error) {
	inst, err := s.requireUnlocked(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	tested := sharkord.FactsFor(inst.SharkordVersion)
	cat, err := s.Catalog.Catalog(ctx)
	if err != nil {
		return nil, nil, err
	}
	staging, err := s.stagingDir(id, "plugin")
	if err != nil {
		return nil, nil, err
	}
	defer os.RemoveAll(staging)
	var all []staged
	var ms []plugins.Manifest
	var current []string
	seen := map[string]bool{}
	for i, pluginID := range pluginIDs {
		if seen[pluginID] {
			continue
		}
		seen[pluginID] = true
		e, ok := cat.Find(pluginID)
		if !ok {
			return nil, nil, fmt.Errorf("there's no Shiver plugin called %q in the plugin list", pluginID)
		}
		rel, err := s.Catalog.Latest(ctx, e)
		if err != nil {
			return nil, nil, err
		}
		if m, ok := plugins.InstalledPlugin(inst.DataPath, e.ID); ok && !selfupdate.Newer(rel.Version, m.Version) {
			current = append(current, fmt.Sprintf("%s already has %s %s, the latest", id, e.Name, m.Version))
			continue
		}
		dir := filepath.Join(staging, fmt.Sprint(i))
		unpacked := filepath.Join(dir, "unpacked")
		if err := os.MkdirAll(unpacked, 0o700); err != nil {
			return nil, nil, err
		}
		archive := filepath.Join(dir, "download")
		if err := s.Catalog.Download(ctx, rel, archive); err != nil {
			return nil, nil, err
		}
		root, m, err := plugins.UnpackFile(archive, unpacked)
		if err != nil {
			return nil, nil, err
		}
		if m.ID != e.ID {
			return nil, nil, fmt.Errorf("%s's release holds the plugin %q, not %q", e.Name, m.ID, e.ID)
		}
		if err := m.Valid(tested.PluginSDK); err != nil {
			return nil, nil, err
		}
		all = append(all, staged{root, m})
		ms = append(ms, m)
	}
	if len(all) == 0 {
		if len(current) == 0 {
			return nil, nil, fmt.Errorf("no plugins given")
		}
		return nil, nil, errors.New(strings.Join(current, "; "))
	}
	notes, err := s.swapPlugins(ctx, id, inst, tested, staging, all)
	return ms, append(current, notes...), err
}
