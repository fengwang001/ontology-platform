// Package step defines a single workflow step: its idempotent actions and
// the state machine driven exclusively by journal records.
package step

import (
	"context"
	"sync"

	"ontology/journal"
)

// Status is the lifecycle state of one step.
type Status int

const (
	Pending          Status = iota // not started
	Running                        // executing (possibly between retries)
	Succeeded                      // finished successfully
	Failed                         // terminally failed
	Compensating                   // compensation in flight
	Compensated                    // compensation succeeded
	CompensateFailed               // compensation failed terminally
)

func (s Status) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Running:
		return "Running"
	case Succeeded:
		return "Succeeded"
	case Failed:
		return "Failed"
	case Compensating:
		return "Compensating"
	case Compensated:
		return "Compensated"
	case CompensateFailed:
		return "CompensateFailed"
	}
	return "Unknown"
}

// Step is the user-supplied definition of one node. Run and Compensate
// must be idempotent: after a crash they may be invoked again for the
// same logical step.
type Step struct {
	ID         string
	Run        func(ctx context.Context) error
	Compensate func(ctx context.Context) error
}

// Machine tracks one step's state. It is mutated only by applying journal
// records (the journal is the single source of truth), so replaying the
// same records always reproduces the same machine. Safe for concurrent
// reads while a worker applies records.
type Machine struct {
	mu     sync.Mutex
	status Status
	exec   int
	comp   int
	note   string
}

// NewMachine returns a machine in the Pending state.
func NewMachine() *Machine { return &Machine{} }

// Apply folds one journal record into the machine. Each call is O(1).
func (m *Machine) Apply(rec journal.Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch rec.Phase {
	case journal.PhaseStart, journal.PhaseRetry:
		m.status = Running
		m.exec++
	case journal.PhaseSuccess:
		m.status = Succeeded
	case journal.PhaseFailure:
		m.status = Failed
		m.note = rec.Note
	case journal.PhaseCompStart:
		m.status = Compensating
		m.comp++
	case journal.PhaseCompOK:
		m.status = Compensated
	case journal.PhaseCompFail:
		m.status = CompensateFailed
		m.note = rec.Note
	}
}

// ResetAfterCrash resolves states left dangling by a crash: a step
// interrupted while Running is retried from Pending; a step interrupted
// while Compensating is compensated again from Succeeded.
func (m *Machine) ResetAfterCrash() {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch m.status {
	case Running:
		m.status = Pending
	case Compensating:
		m.status = Succeeded
	}
}

// Status returns the current state.
func (m *Machine) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

// Counts returns the execution and compensation attempt counters.
func (m *Machine) Counts() (exec, comp int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.exec, m.comp
}

// Note returns the detail stored by the last failure record.
func (m *Machine) Note() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.note
}
