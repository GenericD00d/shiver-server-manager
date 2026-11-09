// Package systemd writes the per-instance drop-ins for sharkord@.service and
// controls units through systemctl.
package systemd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/GenericD00d/shiver-server-manager/internal/run"
)

// Unit is an instance's service name.
func Unit(id string) string { return "sharkord@" + id + ".service" }

// DropinDir is where an instance's drop-ins live under systemdDir
// (/etc/systemd/system).
func DropinDir(systemdDir, id string) string {
	return filepath.Join(systemdDir, "sharkord@"+id+".service.d")
}

// DataDropin lets the hardened unit write to the instance's data folder and
// wait for its drive. RequiresMountsFor names the storage location, not the
// data folder, so an encrypted instance doesn't depend on its own mount.
func DataDropin(marker, id, location, dataPath string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (instance %s). Do not edit.\n", marker, id)
	fmt.Fprintf(&b, "[Unit]\nRequiresMountsFor=%s\n\n[Service]\n", location)
	if location == "/home" || strings.HasPrefix(location, "/home/") {
		b.WriteString("ProtectHome=read-only\n")
	}
	fmt.Fprintf(&b, "Environment=HOME=%s\nReadWritePaths=%s\n", dataPath, dataPath)
	return []byte(b.String())
}

// EncryptedDropin skips the start while the container isn't mounted, so a
// locked instance never sets up an empty community on the bare disk.
func EncryptedDropin(marker, id, dataPath string) []byte {
	return []byte(fmt.Sprintf("# %s (instance %s). Do not edit.\n[Unit]\nConditionPathIsMountPoint=%s\n", marker, id, dataPath))
}

// Limits caps memory and CPU. memoryMaxMiB 0 or cpuPercent 0 leaves that
// one out. MemoryHigh is set at 90% of MemoryMax, so the instance slows
// before it's killed.
func LimitsDropin(marker, id string, memoryMaxMiB, cpuPercent int) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (instance %s). Do not edit.\n[Service]\n", marker, id)
	if memoryMaxMiB > 0 {
		fmt.Fprintf(&b, "MemoryHigh=%dM\nMemoryMax=%dM\n", memoryMaxMiB*9/10, memoryMaxMiB)
	}
	if cpuPercent > 0 {
		fmt.Fprintf(&b, "CPUQuota=%d%%\n", cpuPercent)
	}
	return []byte(b.String())
}

// Systemd runs systemctl.
type Systemd struct{ R run.Runner }

func (s Systemd) ctl(ctx context.Context, args ...string) (run.Result, error) {
	return s.R.Run(ctx, run.Cmd{Name: "systemctl", Args: args})
}

func (s Systemd) DaemonReload(ctx context.Context) error {
	_, err := s.ctl(ctx, "daemon-reload")
	return err
}

func (s Systemd) EnableNow(ctx context.Context, unit string) error {
	_, err := s.ctl(ctx, "enable", "--now", unit)
	return err
}

// DisableNow stops and disables a unit. A unit that's already gone is fine.
func (s Systemd) DisableNow(ctx context.Context, unit string) error {
	_, err := s.ctl(ctx, "disable", "--now", unit)
	if err != nil && strings.Contains(err.Error(), "not loaded") {
		return nil
	}
	return err
}

func (s Systemd) Start(ctx context.Context, unit string) error {
	_, err := s.ctl(ctx, "start", unit)
	return err
}

func (s Systemd) Stop(ctx context.Context, unit string) error {
	_, err := s.ctl(ctx, "stop", unit)
	return err
}

func (s Systemd) Restart(ctx context.Context, unit string) error {
	_, err := s.ctl(ctx, "restart", unit)
	return err
}

// Show reads properties with systemctl show -p.
func (s Systemd) Show(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	args := []string{"show", unit}
	for _, p := range props {
		args = append(args, "-p", p)
	}
	res, err := s.ctl(ctx, args...)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			out[k] = v
		}
	}
	return out, nil
}

// Status is the part of a unit's state the manager shows.
type Status struct {
	Active  string // active, inactive, failed, activating...
	Sub     string
	Enabled string // enabled, disabled...
	// ConditionFailed is true when the last start was skipped by a condition
	// (an encrypted instance whose container isn't mounted).
	ConditionFailed bool
}

func (s Systemd) Status(ctx context.Context, unit string) (Status, error) {
	m, err := s.Show(ctx, unit, "ActiveState", "SubState", "UnitFileState", "ConditionResult")
	if err != nil {
		return Status{}, err
	}
	return Status{Active: m["ActiveState"], Sub: m["SubState"], Enabled: m["UnitFileState"], ConditionFailed: m["ConditionResult"] == "no"}, nil
}

// Logs returns the unit's journal, newest last.
func (s Systemd) Logs(ctx context.Context, unit string, lines int) (string, error) {
	res, err := s.R.Run(ctx, run.Cmd{Name: "journalctl", Args: []string{"-u", unit, "-n", fmt.Sprint(lines), "--no-pager", "-o", "short-iso"}})
	return string(res.Stdout), err
}
