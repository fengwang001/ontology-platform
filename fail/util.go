package fail

import "fmt"

func toString(v any) string { return fmt.Sprintf("%v", v) }

// LessCause reports whether a is the earlier root cause: lower rank wins,
// lower id breaks ties. This total order keeps reports deterministic under
// random completion timing.
func LessCause(a, b Cause) bool {
	if a.Rank != b.Rank {
		return a.Rank < b.Rank
	}
	return a.ID < b.ID
}

// MinCause returns the earlier of a and b. The zero Cause (Rank 0 with empty
// ID) is treated as present only when b is unset; callers pass real causes.
func MinCause(a, b Cause) Cause {
	if LessCause(a, b) {
		return a
	}
	return b
}

// All returns copies of every tracked state keyed by id.
func (t *Tracker) All() map[string]TaskState {
	out := make(map[string]TaskState, len(t.states))
	for id, s := range t.states {
		out[id] = *s
	}
	return out
}

// IDs returns the tracked ids in arbitrary map order; callers sort as needed.
func (t *Tracker) IDs() []string {
	out := make([]string, 0, len(t.states))
	for id := range t.states {
		out = append(out, id)
	}
	return out
}
