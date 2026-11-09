//go:build !linux

package ops

import (
	"context"

	"github.com/GenericD00d/shiver-server-manager/internal/backup"
)

// Development stand-ins: no users to switch to, no /proc.
func vacuumAs(_ context.Context, _, _ int, src, dst string) error { return backup.Vacuum(src, dst) }

func DBInUse(string) (string, error) { return "", nil }
