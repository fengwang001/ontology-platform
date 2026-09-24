// Package report renders deterministic execution reports: one line per
// task, sorted by task ID, independent of completion order.
package report

import (
	"fmt"
	"slices"
	"strings"

	"ontology/fail"
)

// Entry is the final recorded state of one task.
type Entry struct {
	Status fail.Status
	// Reason is the ID of the original failure that caused a Skipped or
	// Cancelled state; for Failed it is the task's own ID.
	Reason string
	// Err is the original error for Failed, or a sentinel for others.
	Err error
}

// Report records the terminal state of every task. Entries are written
// once and never rewritten.
type Report struct {
	entries map[string]Entry
}

// New returns an empty report.
func New() *Report { return &Report{entries: map[string]Entry{}} }

// Set records the terminal state of id. The first write wins: later
// writes for the same id are dropped so cancelled tasks keep their state.
func (r *Report) Set(id string, e Entry) {
	if _, ok := r.entries[id]; ok {
		return
	}
	r.entries[id] = e
}

// Has reports whether id already has a terminal state.
func (r *Report) Has(id string) bool {
	_, ok := r.entries[id]
	return ok
}

// Get returns the recorded entry for id.
func (r *Report) Get(id string) (Entry, bool) {
	e, ok := r.entries[id]
	return e, ok
}

// IDs returns the recorded task IDs in ascending order.
func (r *Report) IDs() []string {
	out := make([]string, 0, len(r.entries))
	for id := range r.entries {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Count returns how many tasks ended in the given status.
func (r *Report) Count(s fail.Status) int {
	n := 0
	for _, e := range r.entries {
		if e.Status == s {
			n++
		}
	}
	return n
}

// String renders the report deterministically: one line per task sorted
// by ID, fields separated by tabs: id, status, reason, error.
func (r *Report) String() string {
	var b strings.Builder
	for _, id := range r.IDs() {
		e := r.entries[id]
		reason, errText := e.Reason, "-"
		if reason == "" {
			reason = "-"
		}
		if e.Err != nil {
			errText = e.Err.Error()
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", id, e.Status, reason, errText)
	}
	return b.String()
}
