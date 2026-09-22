package step

import (
	"errors"

	"ontology/journal"
)

// Func is one idempotent business operation. Func MUST be safe to re-run:
// recovery re-invokes it only when no Done record exists, and implementers
// are expected to make the external effect itself idempotent.
type Func func() error

// Action pairs an execution function with its compensation function.
// Compensate is only ever invoked after a durable Done record, at most once.
type Action struct {
	Execute    Func
	Compensate Func
}

// ErrSkipped is returned when a transition cannot be attempted because the
// machine is not in a state that permits it (used defensively).
var ErrSkipped = errors.New("step: transition skipped due to state")

// Executor drives one transition against the journal and returns the
// resulting terminal status. All state changes appear only as journal
// records; the machine is re-folded from each written record so that
// normal execution and recovery share exactly one code path.
type Executor struct {
	j *journal.Journal
}

// NewExecutor binds an executor to a journal.
func NewExecutor(j *journal.Journal) *Executor { return &Executor{j: j} }

// RunExecute performs one execution attempt guarded by start/done/failed
// records. It never retries itself; the policy layer decides whether to
// call again.
func (e *Executor) RunExecute(m *Machine, act Action) error {
	if m.State() != Pending && m.State() != Running {
		return ErrSkipped
	}
	if _, err := e.j.Append(m.ID(), journal.PhaseStart, ""); err != nil {
		return err
	}
	if err := m.Apply(lastRecord(e.j)); err != nil {
		return err
	}
	err := runFunc(act.Execute)
	if err == nil {
		if _, aerr := e.j.Append(m.ID(), journal.PhaseDone, ""); aerr != nil {
			return aerr
		}
		return m.Apply(lastRecord(e.j))
	}
	return err
}

// MarkFailed writes the terminal failure record (after retries exhausted).
func (e *Executor) MarkFailed(m *Machine, cause error) error {
	if m.State() != Running {
		return ErrSkipped
	}
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	if _, err := e.j.Append(m.ID(), journal.PhaseFailed, msg); err != nil {
		return err
	}
	return m.Apply(lastRecord(e.j))
}

// ResumeExecute continues a Running machine whose latest attempt's start
// record is already durable (crash recovery). It does NOT write another
// start record: the in-flight attempt is re-driven to an outcome, keeping
// execution counts identical to a crash-free run.
func (e *Executor) ResumeExecute(m *Machine, act Action) error {
	if m.State() != Running {
		return ErrSkipped
	}
	err := runFunc(act.Execute)
	if err == nil {
		if _, aerr := e.j.Append(m.ID(), journal.PhaseDone, ""); aerr != nil {
			return aerr
		}
		return m.Apply(lastRecord(e.j))
	}
	return err
}

// ResumeCompensate continues a Compensating machine whose compensation
// start record is already durable, without adding another one.
func (e *Executor) ResumeCompensate(m *Machine, act Action) error {
	if m.State() != Compensating {
		return ErrSkipped
	}
	return e.runCompensateBody(m, act)
}

// RunCompensate performs the single compensation attempt.
func (e *Executor) RunCompensate(m *Machine, act Action) error {
	if m.State() != Done && m.State() != Compensating && m.State() != CompFailed {
		return ErrSkipped
	}
	if m.CompCount() == 0 {
		if _, err := e.j.Append(m.ID(), journal.PhaseCompStart, ""); err != nil {
			return err
		}
		if err := m.Apply(lastRecord(e.j)); err != nil {
			return err
		}
	}
	return e.runCompensateBody(m, act)
}

func (e *Executor) runCompensateBody(m *Machine, act Action) error {
	err := runFunc(act.Compensate)
	if err == nil {
		if _, aerr := e.j.Append(m.ID(), journal.PhaseCompDone, ""); aerr != nil {
			return aerr
		}
		return m.Apply(lastRecord(e.j))
	}
	if _, aerr := e.j.Append(m.ID(), journal.PhaseCompFailed, err.Error()); aerr != nil {
		return aerr
	}
	if aerr := m.Apply(lastRecord(e.j)); aerr != nil {
		return aerr
	}
	return err
}

func runFunc(f Func) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("step: action panicked")
		}
	}()
	if f == nil {
		return nil
	}
	return f()
}

func lastRecord(j *journal.Journal) journal.Record {
	s := j.Snapshot()
	return s[len(s)-1]
}
