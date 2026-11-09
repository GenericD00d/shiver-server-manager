package ops

import (
	"context"
	"errors"
	"fmt"

	"github.com/GenericD00d/shiver-server-manager/internal/state"
)

// Step is one idempotent part of an operation.
type Step struct {
	Name string                              // stable; stored in the record
	Done func(context.Context) (bool, error) // is the result already in place?
	Do   func(context.Context) error         // make it so; safe to repeat
}

// PartialError means an operation stopped partway. Its progress is saved
// in the record, so "repair" (or Retry) can resume it.
type PartialError struct {
	Instance, Op, Step string
	Err                error
}

func (e *PartialError) Error() string {
	return fmt.Sprintf("%s of %s stopped at step %q: %v (run 'repair' or retry to carry on)", e.Op, e.Instance, e.Step, e.Err)
}
func (e *PartialError) Unwrap() error { return e.Err }

// IsPartial reports whether err is a *PartialError.
func IsPartial(err error) bool {
	var p *PartialError
	return errors.As(err, &p)
}

// runSteps runs steps for instance id under operation op. Each step is
// skipped if Done, otherwise run and then checked again. Progress is saved
// after every step. A step may delete the record (the last step of a
// delete); then nothing more is saved.
func (s *Sys) runSteps(ctx context.Context, id, op string, steps []Step) error {
	inst, ok := s.St.Instances[id]
	if !ok {
		return fmt.Errorf("there's no server called %q", id)
	}
	if inst.Operation == nil || inst.Operation.Name != op {
		inst.Operation = &state.Operation{Name: op, StartedAt: s.Now().UTC()}
	}
	inst.Operation.FailedStep, inst.Operation.Error = "", ""
	if err := s.Save(); err != nil {
		return err
	}
	fail := func(step string, err error) error {
		s.emit(Event{Instance: id, Op: op, Step: step, Status: "fail", Detail: err.Error()})
		if inst, ok := s.St.Instances[id]; ok && inst.Operation != nil {
			inst.Operation.FailedStep, inst.Operation.Error = step, err.Error()
			if serr := s.Save(); serr != nil {
				err = errors.Join(err, serr)
			}
		}
		return &PartialError{Instance: id, Op: op, Step: step, Err: err}
	}
	for _, st := range steps {
		if err := ctx.Err(); err != nil {
			return fail(st.Name, err)
		}
		done, err := st.Done(ctx)
		if err != nil {
			return fail(st.Name, err)
		}
		if done {
			s.emit(Event{Instance: id, Op: op, Step: st.Name, Status: "skip"})
		} else {
			s.emit(Event{Instance: id, Op: op, Step: st.Name, Status: "run"})
			if err := st.Do(ctx); err != nil {
				return fail(st.Name, err)
			}
			if done, err = st.Done(ctx); err != nil || !done {
				if err == nil {
					err = errors.New("the step ran but its result isn't there")
				}
				return fail(st.Name, err)
			}
			s.emit(Event{Instance: id, Op: op, Step: st.Name, Status: "done"})
		}
		inst, ok := s.St.Instances[id]
		if !ok {
			return nil // the record was removed by this step
		}
		if inst.Operation != nil {
			inst.Operation.LastStep = st.Name
		}
		if err := s.Save(); err != nil {
			return err
		}
	}
	if inst, ok := s.St.Instances[id]; ok {
		inst.Operation = nil
		return s.Save()
	}
	return nil
}

// Resume carries on with an instance's saved operation.
func (s *Sys) Resume(ctx context.Context, id string) error {
	inst, err := s.Instance(id)
	if err != nil {
		return err
	}
	if inst.Operation == nil {
		return nil
	}
	steps, err := s.stepsFor(id, inst.Operation.Name)
	if err != nil {
		return err
	}
	return s.runSteps(ctx, id, inst.Operation.Name, steps)
}

// stepsFor rebuilds an operation's steps from the record alone.
func (s *Sys) stepsFor(id, op string) ([]Step, error) {
	switch op {
	case OpCreate:
		return s.createSteps(id), nil
	case OpDelete:
		return s.deleteSteps(id), nil
	}
	return nil, fmt.Errorf("unknown operation %q in the records", op)
}

// Operation names.
const (
	OpCreate = "create"
	OpDelete = "delete"
)
