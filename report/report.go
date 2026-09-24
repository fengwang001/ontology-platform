// Package report renders deterministic per-task execution reports.
package report

import (
	"fmt"
	"sort"
	"strings"

	"ontology/fail"
)

// Entry is one task's final state and reason.
type Entry struct {
	State fail.State
	Err   error
}

// Report maps task IDs to their terminal entries.
type Report struct{ entries map[string]Entry }

func New() *Report { return &Report{entries: map[string]Entry{}} }

// Set records a task's terminal state and reason (nil for success).
func (r *Report) Set(id string, s fail.State, err error) {
	r.entries[id] = Entry{State: s, Err: err}
}

// Entry returns a task's entry.
func (r *Report) Entry(id string) (Entry, bool) {
	e, ok := r.entries[id]
	return e, ok
}

// Count returns how many tasks ended in state s.
func (r *Report) Count(s fail.State) int {
	n := 0
	for _, e := range r.entries {
		if e.State == s {
			n++
		}
	}
	return n
}

// String renders one line per task, sorted by ID: byte-identical for
// identical outcomes regardless of completion order.
func (r *Report) String() string {
	ids := make([]string, 0, len(r.entries))
	for id := range r.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		e := r.entries[id]
		reason := "-"
		if e.Err != nil {
			reason = e.Err.Error()
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\n", id, e.State, reason)
	}
	return b.String()
}
