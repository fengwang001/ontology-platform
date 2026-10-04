// Package ring implements a fixed-capacity counting sliding window over
// call outcomes. Failure and slow counters are maintained incrementally,
// so every operation examines a constant number of slots.
package ring

type entry struct {
	fail bool
	slow bool
}

// Ring is a circular buffer of the most recent call outcomes.
type Ring struct {
	slots []entry
	head  int // index of the oldest element
	count int
	fails int
	slows int

	// examined counts slot accesses; tests use it to prove that Add
	// examines O(1) elements regardless of capacity.
	examined int64
}

// New returns an empty window of capacity n (n >= 1).
func New(n int) *Ring {
	return &Ring{slots: make([]entry, n)}
}

// Add records one outcome, evicting the oldest entry when full.
func (r *Ring) Add(fail, slow bool) {
	if r.count == len(r.slots) {
		old := r.slots[r.head]
		r.examined++
		if old.fail {
			r.fails--
		}
		if old.slow {
			r.slows--
		}
		r.head = (r.head + 1) % len(r.slots)
		r.count--
	}
	tail := (r.head + r.count) % len(r.slots)
	r.slots[tail] = entry{fail: fail, slow: slow}
	r.examined++
	if fail {
		r.fails++
	}
	if slow {
		r.slows++
	}
	r.count++
}

// Count returns the number of entries currently in the window.
func (r *Ring) Count() int { return r.count }

// Failures returns the number of failures currently in the window.
func (r *Ring) Failures() int { return r.fails }

// Slows returns the number of slow calls currently in the window.
func (r *Ring) Slows() int { return r.slows }

// Clear empties the window in O(1).
func (r *Ring) Clear() {
	r.slots = make([]entry, len(r.slots))
	r.head, r.count, r.fails, r.slows = 0, 0, 0, 0
}
