package osuser

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"strconv"

	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/validate"
)

// InstanceUser is a server's system user and group name.
func InstanceUser(id string) string { return paths.InstanceUserPrefix + id }

// UserExists asks getent (exit 2 means not found).
func UserExists(ctx context.Context, r run.Runner, name string) (bool, error) {
	_, err := r.Run(ctx, run.Cmd{Name: "getent", Args: []string{"passwd", name}})
	var exitErr *run.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode == 2 {
		return false, nil
	}
	return err == nil, err
}

// EnsureInstanceUser creates sharkord-<id> as a system user with its own
// group, no login shell and home at the data folder.
func EnsureInstanceUser(ctx context.Context, r run.Runner, id, home string) error {
	if err := validate.InstanceID(id); err != nil {
		return err
	}
	name := InstanceUser(id)
	ok, err := UserExists(ctx, r, name)
	if err != nil || ok {
		return err
	}
	_, err = r.Run(ctx, run.Cmd{Name: "useradd", Args: []string{
		"--system", "--user-group", "--no-create-home", "--home-dir", home,
		"--shell", "/usr/sbin/nologin", name,
	}})
	return err
}

// DeleteInstanceUser removes sharkord-<id> and its group. Gone is fine.
func DeleteInstanceUser(ctx context.Context, r run.Runner, id string) error {
	if err := validate.InstanceID(id); err != nil {
		return err
	}
	name := InstanceUser(id)
	ok, err := UserExists(ctx, r, name)
	if err != nil || !ok {
		return err
	}
	_, err = r.Run(ctx, run.Cmd{Name: "userdel", Args: []string{name}})
	return err
}

// IDs looks up a user's UID and primary GID.
func IDs(name string) (uid, gid int, err error) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, fmt.Errorf("look up %s: %w", name, err)
	}
	uid, err = strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, err
	}
	gid, err = strconv.Atoi(u.Gid)
	return uid, gid, err
}
