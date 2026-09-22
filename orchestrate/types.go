// Package orchestrate wires graph, journal, step and policy into a
// workflow engine: layered concurrent scheduling, reverse-topological
// compensation, and crash recovery from the journal alone.
package orchestrate

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/journal"
	"ontology/step"
)

// ErrTooManySteps rejects a graph larger than Limits.MaxSteps.
var ErrTooManySteps = errors.New("orchestrate: too many steps")

// ErrJournalTooLong rejects appends beyond Limits.MaxJournalRecords.
var ErrJournalTooLong = journal.ErrTooLong

// ErrConcurrencyLimit rejects an out-of-range Limits.MaxConcurrency.
var ErrConcurrencyLimit = errors.New("orchestrate: concurrency limit out of range")

// ErrCrashed is returned by Run when an injected crash hook fires.
var ErrCrashed = errors.New("orchestrate: simulated crash")

// HardMaxConcurrency bounds Limits.MaxConcurrency.
const HardMaxConcurrency = 1024

// DefaultConcurrency is used when Limits.MaxConcurrency is zero.
const DefaultConcurrency = 64

// Limits caps resource usage. Zero MaxSteps / MaxJournalRecords mean
// unlimited; zero MaxConcurrency selects DefaultConcurrency.
type Limits struct {
	MaxSteps          int
	MaxJournalRecords int
	MaxConcurrency    int
}

// CrashSignal is the panic value a crash hook must use. The orchestrator
// recovers it, unwinds safely, and makes Run return ErrCrashed.
type CrashSignal struct{}

// Phase is the externally visible run phase. The only terminal phases
// are PhaseCompleted and PhaseCompensated.
type Phase int

const (
	PhaseIdle Phase = iota
	PhaseRunning
	PhaseCompleted   // every step succeeded
	PhaseCompensated // a step failed; all succeeded steps were compensated
)

func (p Phase) String() string {
	switch p {
	case PhaseIdle:
		return "Idle"
	case PhaseRunning:
		return "Running"
	case PhaseCompleted:
		return "Completed"
	case PhaseCompensated:
		return "Compensated"
	}
	return "Unknown"
}

// StepInfo is the read-only view of one step. The zero value is what
// queries return for unregistered step IDs.
type StepInfo struct {
	Status    step.Status
	ExecCount int
	CompCount int
}

// Snapshot is a consistent, read-only view of the orchestrator.
type Snapshot struct {
	Phase      Phase
	Steps      map[string]StepInfo
	JournalLen int
}

// FailureError is returned by Run when a step failed terminally. The run
// then ends in PhaseCompensated; Compensation aggregates compensation
// failures, if any.
type FailureError struct {
	Step         string
	Err          error
	Compensation error
}

func (e *FailureError) Error() string {
	return fmt.Sprintf("orchestrate: step %q failed: %v", e.Step, e.Err)
}

// Unwrap exposes the underlying step error.
func (e *FailureError) Unwrap() error { return e.Err }

// CompensationError aggregates every failed compensation, keyed by step.
type CompensationError struct {
	Failures map[string]error
}

func (e *CompensationError) Error() string {
	ids := make([]string, 0, len(e.Failures))
	for id := range e.Failures {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	b.WriteString("orchestrate: compensation failures:")
	for _, id := range ids {
		fmt.Fprintf(&b, " %s=%v;", id, e.Failures[id])
	}
	return b.String()
}
