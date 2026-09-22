package step

import (
	"fmt"

	"ontology/journal"
)

// Machine is the reconstructed state of one step. Machines contain only
// facts derivable from journal records; identical records always yield an
// identical Machine (idempotent replay).
type Machine struct {
	id      string
	state   State
	starts  int // execution starts (attempts)
	done    bool
	failed  bool
	cStarts int // compensation starts
	cDone   bool
	cFailed bool
	errMsg  string
	cErrMsg string
}

// NewMachine returns a fresh machine for step id in Pending.
func NewMachine(id string) *Machine { return &Machine{id: id} }

// ID returns the step id.
func (m *Machine) ID() string { return m.id }

// State returns the current state.
func (m *Machine) State() State { return m.state }

// ExecCount returns how many execution attempts began.
func (m *Machine) ExecCount() int { return m.starts }

// CompCount returns how many compensation attempts began.
func (m *Machine) CompCount() int { return m.cStarts }

// FailMessage returns the recorded terminal failure detail ("" if none).
func (m *Machine) FailMessage() string { return m.errMsg }

// CompFailMessage returns the recorded compensation failure detail.
func (m *Machine) CompFailMessage() string { return m.cErrMsg }

// Apply folds one journal record into the machine. It is the single state
// transition authority: normal execution and recovery use exactly this path.
func (m *Machine) Apply(r journal.Record) error {
	if r.StepID != m.id {
		return fmt.Errorf("step: record for %q applied to machine %q", r.StepID, m.id)
	}
	switch r.Phase {
	case journal.PhaseStart:
		if m.state != Pending && m.state != Running {
			return fmt.Errorf("step %q: start in state %s", m.id, m.state)
		}
		m.starts++
		m.state = Running
	case journal.PhaseDone:
		if m.state != Running {
			return fmt.Errorf("step %q: done in state %s", m.id, m.state)
		}
		if m.done {
			return fmt.Errorf("step %q: duplicate done", m.id)
		}
		m.done = true
		m.state = Done
	case journal.PhaseFailed:
		if m.state != Running {
			return fmt.Errorf("step %q: failed in state %s", m.id, m.state)
		}
		m.failed = true
		m.errMsg = r.Detail
		m.state = Failed
	case journal.PhaseCompStart:
		if m.state != Done && m.state != Compensating && m.state != CompFailed {
			return fmt.Errorf("step %q: compensation start in state %s", m.id, m.state)
		}
		m.cStarts++
		m.cFailed = false
		m.state = Compensating
	case journal.PhaseCompDone:
		if m.state != Compensating || !m.done {
			return fmt.Errorf("step %q: compensation done in state %s", m.id, m.state)
		}
		m.cDone = true
		m.state = Compensated
	case journal.PhaseCompFailed:
		if m.state != Compensating || !m.done {
			return fmt.Errorf("step %q: compensation failure in state %s", m.id, m.state)
		}
		m.cFailed = true
		m.cErrMsg = r.Detail
		m.state = CompFailed
	default:
		return fmt.Errorf("step %q: unknown phase %d", m.id, r.Phase)
	}
	return nil
}

// NeedsResume reports what the orchestrator must (re)do after recovery:
// a dangling start with no terminal record. Done/Failed steps never resume
// execution; a dangling compensation start resumes compensation.
func (m *Machine) NeedsResume() bool {
	return m.state == Running || m.state == Compensating
}

// NeedsCompensation reports whether this completed step still has to be
// compensated after recovery (completed, no compensation record at all).
func (m *Machine) NeedsCompensation() bool {
	return m.state == Done
}
