package cli

import (
	"fmt"

	"github.com/GenericD00d/shiver-server-manager/internal/ops"
	"github.com/GenericD00d/shiver-server-manager/internal/paths"
	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

// withOps logs in, takes the lock, loads the records and hands fn an
// ops.Sys that prints progress.
func (a *App) withOps(mode state.LockMode, fn func(s *ops.Sys) error) error {
	if err := a.login(); err != nil {
		return err
	}
	lk, err := a.lock(mode)
	if err != nil {
		return err
	}
	defer lk.Release()
	st, err := state.Load(paths.StateFile)
	if err != nil {
		return err
	}
	s := ops.New(a.runner, st, a.log, paths.Cmd+"/"+a.Version)
	s.Progress = a.printEvent
	return fn(s)
}

// logged runs fn and writes the result to the action log.
func (a *App) logged(action, id string, fn func() error) error {
	err := fn()
	a.log.Action(action, err, "instance", id)
	return err
}

func (a *App) printEvent(e ops.Event) {
	if a.Globals.JSON {
		return
	}
	switch e.Status {
	case "run":
		fmt.Fprintf(a.Out, "  … %s\n", e.Step)
	case "done":
		if e.Detail != "" {
			fmt.Fprintf(a.Out, "  ✓ %s (%s)\n", e.Step, e.Detail)
		} else {
			fmt.Fprintf(a.Out, "  ✓ %s\n", e.Step)
		}
	case "skip":
		fmt.Fprintf(a.Out, "  ✓ %s (already done)\n", e.Step)
	case "fail":
		fmt.Fprintf(a.Out, "  ✗ %s: %s\n", e.Step, e.Detail)
	case "note":
		fmt.Fprintf(a.Out, "  ! %s\n", e.Detail)
	}
}
