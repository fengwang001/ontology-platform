// Package report renders the deterministic final-state report of a run.
package report

import (
	"errors"
	"sort"
	"strings"

	"ontology/fail"
)

// Result is the final state of one task.
type Result struct {
	Task   string
	State  fail.State
	Err    error
	Origin string // earliest failed upstream for Skipped tasks
}

// Report is the ordered collection of task results plus run statistics.
type Report struct {
	Results []Result

	// PeakRun is the historical maximum number of concurrently running tasks.
	PeakRun int
	// Decisions is the total number of ready-decision steps made.
	Decisions int
}

// ByTask returns the result for a task and whether it was present.
func (r *Report) ByTask(id string) (Result, bool) {
	for _, res := range r.Results {
		if res.Task == id {
			return res, true
		}
	}
	return Result{}, false
}

// StateOf returns the state of id, or fail.Pending if unknown.
func (r *Report) StateOf(id string) fail.State {
	if res, ok := r.ByTask(id); ok {
		return res.State
	}
	return fail.Pending
}

// Render produces deterministic text: one line per task, sorted by ID.
// Timing and statistics never appear here, so identical inputs render to
// identical bytes regardless of completion order.
func (r *Report) Render() string {
	rows := make([]Result, len(r.Results))
	copy(rows, r.Results)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Task < rows[j].Task })
	var b strings.Builder
	for _, res := range rows {
		b.WriteString(res.Task)
		b.WriteByte('\t')
		b.WriteString(res.State.String())
		switch res.State {
		case fail.Skipped:
			b.WriteString("\torigin=")
			b.WriteString(res.Origin)
		case fail.Failed, fail.Canceled:
			if res.Err != nil {
				b.WriteString("\t")
				b.WriteString(res.Err.Error())
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Count returns how many tasks are in the given state.
func (r *Report) Count(s fail.State) int {
	n := 0
	for _, res := range r.Results {
		if res.State == s {
			n++
		}
	}
	return n
}

// OriginOf returns the skip origin of id.
func (r *Report) OriginOf(id string) string {
	if res, ok := r.ByTask(id); ok {
		return res.Origin
	}
	return ""
}

// FailedErrors returns the raw errors of all Failed tasks, in ID order.
func (r *Report) FailedErrors() []error {
	rows := make([]Result, len(r.Results))
	copy(rows, r.Results)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Task < rows[j].Task })
	var errs []error
	for _, res := range rows {
		if res.State == fail.Failed {
			errs = append(errs, res.Err)
		}
	}
	return errs
}

// IsPanicFailure reports whether id failed due to a recovered panic.
func (r *Report) IsPanicFailure(id string) bool {
	res, ok := r.ByTask(id)
	if !ok || res.State != fail.Failed {
		return false
	}
	var pe *fail.PanicError
	return errors.As(res.Err, &pe)
}
