// Package q implements priority-grouped FIFO queues and the pure
// operation of picking the highest-priority non-empty queue.
package q

// Queues groups event IDs into one FIFO queue per priority.
type Queues struct {
	m     map[int][]int
	prios []int // distinct live priorities, descending
}

// New returns an empty set of queues.
func New() *Queues { return &Queues{m: make(map[int][]int)} }

// Add appends id to the FIFO queue of prio.
func (qs *Queues) Add(prio, id int) {
	if _, ok := qs.m[prio]; !ok {
		i := 0
		for i < len(qs.prios) && qs.prios[i] > prio {
			i++
		}
		qs.prios = append(qs.prios, 0)
		copy(qs.prios[i+1:], qs.prios[i:])
		qs.prios[i] = prio
	}
	qs.m[prio] = append(qs.m[prio], id)
}

// At returns the ID at index pos in prio's queue.
func (qs *Queues) At(prio, pos int) int { return qs.m[prio][pos] }

// Len returns the length of prio's queue (0 if absent).
func (qs *Queues) Len(prio int) int { return len(qs.m[prio]) }

// Drop removes prio's queue entirely (called once it is exhausted).
func (qs *Queues) Drop(prio int) {
	if _, ok := qs.m[prio]; !ok {
		return
	}
	delete(qs.m, prio)
	for i, p := range qs.prios {
		if p == prio {
			qs.prios = append(qs.prios[:i], qs.prios[i+1:]...)
			return
		}
	}
}

// Highest returns the highest priority that still has a non-empty queue.
// checked reports how many priority buckets were examined: selection is
// by priority bucket, never a scan over individual events.
func (qs *Queues) Highest() (prio, checked int, ok bool) {
	for _, p := range qs.prios {
		checked++
		if len(qs.m[p]) > 0 {
			return p, checked, true
		}
	}
	return 0, checked, false
}

// Snapshot returns a deep copy of all live queues.
func (qs *Queues) Snapshot() map[int][]int {
	out := make(map[int][]int, len(qs.m))
	for p, ids := range qs.m {
		out[p] = append([]int(nil), ids...)
	}
	return out
}
