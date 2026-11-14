package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/sharkord"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
	"github.com/GenericD00d/shiver-server-manager/internal/tui"
)

// menuHost lets the menu drive the app.
type menuHost struct{ a *App }

func (a *App) runMenu() error {
	if !a.term.isTerminal() {
		return fmt.Errorf("the menu needs a terminal; run '%s --help' for commands", paths.Cmd)
	}
	return tui.Run(menuHost{a})
}

func (h menuHost) Login() error {
	h.a.session = nil
	if h.a.needsLogin() {
		fmt.Fprintf(h.a.Err, "\n%s\n", paths.AppName)
	}
	return h.a.login()
}

func (h menuHost) Logout() { h.a.session = nil }

func (h menuHost) Run(args []string, reauth bool) int {
	if reauth && h.a.needsLogin() {
		h.a.session = nil
		fmt.Fprintln(h.a.Err, "This needs your password again.")
	}
	return h.a.Execute(args)
}

func (h menuHost) Snapshot() (tui.Snapshot, error) {
	a := h.a
	snap := tui.Snapshot{User: "root", Backups: map[string][]string{}}
	if a.session != nil {
		snap.User = a.session.User
	}
	for _, s := range sharkord.Settings {
		snap.Settings = append(snap.Settings, strings.TrimPrefix(s.Name, "SHARKORD_"))
	}
	err := a.withOps(state.Shared, func(s *ops.Sys) error {
		ctx := context.Background()
		for _, id := range s.IDs() {
			info, err := s.Info(ctx, id)
			if err != nil {
				return err
			}
			running := info.Active
			switch {
			case info.Locked:
				running = "locked"
			case info.Operation != nil:
				running = "unfinished"
			case info.Stopped:
				running = "stopped"
			case info.Active == "active" && !info.Healthy:
				running = "not answering"
			case info.Active == "active":
				running = "running"
			}
			cert := "pending"
			if ci, err := s.CertInfo(ctx, id); err == nil && ci.Present {
				cert = fmt.Sprintf("%d days", ci.DaysLeft)
			}
			snap.Servers = append(snap.Servers, tui.Server{ID: id, Running: running, Domain: info.Domain, Cert: cert, Location: info.Location, Version: info.SharkordVersion})
			if list, err := s.Backups(id); err == nil {
				for _, e := range list {
					snap.Backups[id] = append(snap.Backups[id], filepath.Join(s.BackupDir(id), e.File))
				}
			}
		}
		for _, l := range s.St.Settings.Locations {
			snap.Locations = append(snap.Locations, l.Path)
		}
		snap.DefaultLocation = s.St.DefaultLocation()
		// cached for an hour, so the menu doesn't ask GitHub on every refresh
		if rs, err := s.Sharkord.Releases(ctx); err == nil {
			for _, r := range rs {
				snap.Versions = append(snap.Versions, r.Version)
			}
			slices.SortFunc(snap.Versions, func(x, y string) int { return sharkord.CompareVersions(y, x) })
		}
		for _, n := range ops.LoadReport(s.Paths.HealthFile).Notices {
			if n.Level != "info" || strings.HasPrefix(n.Key, "locked") {
				snap.Notices = append(snap.Notices, n.Message)
			}
		}
		if locked := s.Locked(ctx); len(locked) > 0 {
			snap.Notices = append([]string{fmt.Sprintf("Encrypted servers wait to be unlocked (%s): Security > Unlock the vault", strings.Join(locked, ", "))}, snap.Notices...)
		}
		return nil
	})
	return snap, err
}
