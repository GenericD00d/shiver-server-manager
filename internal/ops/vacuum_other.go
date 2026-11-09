//go:build !linux

package ops

import (
	"context"
	"fmt"

	"github.com/GenericD00d/shiver-server-manager/internal/backup"
	"github.com/GenericD00d/shiver-server-manager/internal/plugins"
)

// Development stand-ins: no users to switch to, no /proc.
func runSelfAs(_ context.Context, _, _ int, args ...string) error {
	switch args[0] {
	case "internal-vacuum":
		return backup.Vacuum(args[1], args[2])
	case "internal-enable-plugins":
		return plugins.EnableInDB(args[1])
	}
	return fmt.Errorf("unknown internal command %s", args[0])
}

func DBInUse(string) (string, error) { return "", nil }
