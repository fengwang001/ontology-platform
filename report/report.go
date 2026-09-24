// Package report defines task terminal states and deterministic reports.
package report

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// State is the final state of a task.
type State int

const (
	// Succeeded: completed without error before any failure propagation.
	Succeeded State = iota + 1
	// Failed: executed and returned an error (panic included).
	Failed
	// Skipped: never started because of failure propagation.
	Skipped
	// Canceled: had started running and was interrupted after a failure.
	Canceled
)

func (s State) String() string {
	switch s {
	case Succeeded:
		return "SUCCEEDED"
	case Failed:
		return "FAILED"
	case Skipped:
		return "SKIPPED"
	case Canceled:
		return "CANCELED"
	default:
		return "UNKNOWN"
	}
}

// SkipKind distinguishes why a never-started task was skipped.
type SkipKind int

const (
	// DependencyFailed: the task has a failed ancestor (Reason.Cause is the root one).
	DependencyFailed SkipKind = iota + 1
	// Aborted: fail-fast stopped the whole run; task had no failed ancestor.
	Aborted
)

func (k SkipKind) String() string {
	if k == Aborted {
		return "ABORTED"
	}
	return "DEPENDENCY_FAILED"
}

// Reason explains a non-success terminal state.
type Reason struct {
	Kind  SkipKind
	Cause string
	Err   error
}

// TaskResult is one task's final outcome.
type TaskResult struct {
	ID     string
	State  State
	Reason *Reason
}

// Report is the full run result, rendered deterministically by task id.
type Report struct {
	Results []TaskResult
}

// ErrSkipCause lets callers inspect skip causes via errors.Is/As chains if needed.
var ErrSkipCause = errors.New("report: skipped due to failure")

// Get returns the result for an id.
func (r *Report) Get(id string) (TaskResult, bool) {
	for _, x := range r.Results {
		if x.ID == id {
			return x, true
		}
	}
	return TaskResult{}, false
}

// StateOf is a convenience accessor.
func (r *Report) StateOf(id string) State {
	if x, ok := r.Get(id); ok {
		return x.State
	}
	return 0
}

// Render produces byte-stable text: one line per task in id order.
func (r *Report) Render() string {
	rs := append([]TaskResult(nil), r.Results...)
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
	var b strings.Builder
	for _, x := range rs {
		fmt.Fprintf(&b, "%s\t%s", x.ID, x.State)
		if x.Reason != nil {
			switch x.State {
			case Skipped:
				fmt.Fprintf(&b, "\t%s\tcause=%s", x.Reason.Kind, x.Reason.Cause)
			case Failed, Canceled:
				if x.Reason.Err != nil {
					fmt.Fprintf(&b, "\t%s", x.Reason.Err.Error())
				}
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Counts returns a state histogram.
func (r *Report) Counts() map[State]int {
	m := map[State]int{}
	for _, x := range r.Results {
		m[x.State]++
	}
	return m
}
