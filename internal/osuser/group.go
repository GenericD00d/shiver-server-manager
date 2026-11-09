// Package osuser manages OS accounts: the admins group now, and each
// server's system user later.
package osuser

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"slices"
	"strings"

	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/run"
	"github.com/GenericD00d/shiver-server-manager/internal/validate"
)

// Group manages one OS group through getent, groupadd and gpasswd.
type Group struct {
	Name string
	R    run.Runner
	// UserExists defaults to an os/user lookup.
	UserExists func(name string) bool
}

func Admins(r run.Runner) *Group { return &Group{Name: paths.AdminGroup, R: r} }

var ErrLastAdmin = errors.New("refusing to remove the last admin; add another admin first")

func (g *Group) userExists(name string) bool {
	if g.UserExists != nil {
		return g.UserExists(name)
	}
	_, err := user.Lookup(name)
	return err == nil
}

// getent returns the group line's member list, or ok=false if the group
// doesn't exist (getent exits 2 for "not found").
func (g *Group) getent(ctx context.Context) (members []string, ok bool, err error) {
	res, err := g.R.Run(ctx, run.Cmd{Name: "getent", Args: []string{"group", g.Name}})
	var exitErr *run.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode == 2 {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	line := strings.TrimSpace(strings.SplitN(string(res.Stdout), "\n", 2)[0])
	fields := strings.Split(line, ":")
	if len(fields) != 4 || fields[0] != g.Name {
		return nil, false, fmt.Errorf("unexpected getent output %q", line)
	}
	for _, m := range strings.Split(fields[3], ",") {
		if m = strings.TrimSpace(m); m != "" {
			members = append(members, m)
		}
	}
	return members, true, nil
}

func (g *Group) Exists(ctx context.Context) (bool, error) {
	_, ok, err := g.getent(ctx)
	return ok, err
}

// Ensure creates the group as a system group if it's missing.
func (g *Group) Ensure(ctx context.Context) error {
	ok, err := g.Exists(ctx)
	if err != nil || ok {
		return err
	}
	_, err = g.R.Run(ctx, run.Cmd{Name: "groupadd", Args: []string{"--system", g.Name}})
	return err
}

// Members lists the group's named members (not users who only have it as
// their primary group). A missing group has no members.
func (g *Group) Members(ctx context.Context) ([]string, error) {
	m, _, err := g.getent(ctx)
	return m, err
}

// Add puts an existing OS account in the group. Already a member is fine.
func (g *Group) Add(ctx context.Context, name string) error {
	if err := validate.Username(name); err != nil {
		return err
	}
	if !g.userExists(name) {
		return fmt.Errorf("there's no OS account called %s; create it first with: sudo adduser %s", name, name)
	}
	members, err := g.Members(ctx)
	if err != nil {
		return err
	}
	if slices.Contains(members, name) {
		return nil
	}
	_, err = g.R.Run(ctx, run.Cmd{Name: "gpasswd", Args: []string{"-a", name, g.Name}})
	return err
}

// Remove takes name out of the group, refusing to remove the last member.
// Not a member is fine.
func (g *Group) Remove(ctx context.Context, name string) error {
	if err := validate.Username(name); err != nil {
		return err
	}
	members, err := g.Members(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(members, name) {
		return nil
	}
	if len(members) == 1 {
		return ErrLastAdmin
	}
	_, err = g.R.Run(ctx, run.Cmd{Name: "gpasswd", Args: []string{"-d", name, g.Name}})
	return err
}
