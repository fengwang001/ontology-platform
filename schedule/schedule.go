// Package schedule turns a matched hook set into a reproducible execution order:
// all pre hooks precede all post hooks; within a phase, higher priority first,
// then earlier registration order.
package schedule

import (
	"sort"

	"ontology/hook"
)

// Plan returns the deterministic execution sequence for matched hooks.
func Plan(hooks []hook.Hook) []hook.Hook {
	planned := make([]hook.Hook, len(hooks))
	copy(planned, hooks)
	sort.SliceStable(planned, func(i, j int) bool {
		return less(planned[i], planned[j])
	})
	return planned
}

func less(a, b hook.Hook) bool {
	if a.Phase() != b.Phase() {
		return a.Phase() == hook.PhasePre // pre before post
	}
	if a.Priority() != b.Priority() {
		return a.Priority() > b.Priority() // higher priority first
	}
	return a.Registered() < b.Registered() // registration order tie-break
}

type Entry struct {
	Phase    string
	Name     string
	Priority int
}

// Signature converts a plan to a value that can be compared byte-for-byte.
func Signature(hooks []hook.Hook) []Entry {
	out := make([]Entry, len(hooks))
	for i, h := range hooks {
		out[i] = Entry{Phase: h.Phase().String(), Name: h.Name(), Priority: h.Priority()}
	}
	return out
}
