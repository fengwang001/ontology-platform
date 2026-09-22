// Package step defines per-step lifecycle states and the pure fold that
// reconstructs them from journal records. It depends only on journal.
package step

import "ontology/journal"

// State is the externally visible state of one step.
type State string

const (
	// Pending: never started.
	Pending State = "pending"
	// Running: an execution attempt started but no outcome is recorded.
	Running State = "running"
	// Completed: the step action succeeded.
	Completed State = "completed"
	// Failed: execution failed terminally and the step never succeeded.
	Failed State = "failed"
	// Compensating: compensation has started but not finished.
	Compensating State = "compensating"
	// Compensated: the completed step was undone successfully.
	Compensated State = "compensated"
	// CompFailed: compensation ran and its last attempt failed.
	CompFailed State = "comp_failed"
)

// Status holds everything replay can derive about one step.
type Status struct {
	State      State
	ExecStarts int
	ExecDone   int
	CompStarts int
	CompDone   int
	LastErr    string
}

// ExecCount is the number of execution attempts actually initiated.
func (s Status) ExecCount() int { return s.ExecStarts }

// CompCount is the number of compensation attempts actually initiated.
func (s Status) CompCount() int { return s.CompStarts }

// Fold applies one journal record to one step status. It is pure, so replay is
// deterministic and repeatable with identical results.
func Fold(s Status, r journal.Record) Status {
	switch r.Phase {
	case journal.ExecStart:
		s.ExecStarts++
		s.State = Running
	case journal.ExecDone:
		s.ExecDone++
	if r.OK {
			s.State = Completed
			s.LastErr = ""
		} else {
			s.State = Failed
			s.LastErr = r.Err
		}
	case journal.CompStart:
		s.CompStarts++
		s.State = Compensating
	case journal.CompDone:
		s.CompDone++
	if r.OK {
			s.State = Compensated
			s.LastErr = ""
		} else {
			s.State = CompFailed
			s.LastErr = r.Err
		}
	}
	return s
}

// NeedsCompensation reports whether the step still has an un-finished undo
// obligation: completed but never undone, or interrupted/failed mid-undo.
func (s Status) NeedsCompensation() bool {
	switch s.State {
	case Completed, Compensating, CompFailed:
		return true
	default:
		return false
	}
}

// IsTerminal reports whether the step has reached a final state.
func (s Status) IsTerminal() bool {
	switch s.State {
	case Completed, Failed, Compensated, CompFailed:
		return true
	default:
		return false
	}
}
