package rta

import "sync"

// Analyzer is a concurrency-safe fixed-priority response-time analyzer.
type Analyzer struct {
	mu sync.RWMutex

	tasks []Task // append-only accepted task records; indices are stable
	index map[string]int
	order []int // task indices, highest priority first
	resp  map[int]int64

	// sumTerms counts every interference term evaluated by a fixed-point
	// step (one count per higher-priority task per step). Response never
	// increments it, so tests can assert Response does no iteration.
	sumTerms uint64
}

// NewAnalyzer creates an empty analyzer.
func NewAnalyzer() *Analyzer {
	return &Analyzer{index: map[string]int{}, resp: map[int]int64{}}
}

// recomputeFrom recalculates response times for order positions p and later,
// storing results in scratch indexed by task position in tasks. It performs
// exactly len(hp) interference-term evaluations per fixed-point step for
// each task. Nothing cached is mutated on failure.
func recomputeFrom(tasks []Task, order []int, p int, scratch []int64, termCount *uint64) bool {
	for pos := p; pos < len(order); pos++ {
		idx := order[pos]
		_, r, ok := response(tasks, idx, order[:pos], termCount)
		if !ok {
			return false
		}
		scratch[idx] = r
	}
	return true
}

// audsley assigns priorities to all tasks from the lowest level upward: at
// each level it picks the smallest-ID (byte order) candidate that is
// schedulable with every other unassigned task above it. On success it
// returns the high-to-low order and cached response times. On failure it
// returns pending > 0: the number of tasks unassigned at the failing level
// including the level being filled.
func audsley(tasks []Task, termCount *uint64) (order []int, resp map[int]int64, pending int) {
	n := len(tasks)
	remaining := make(map[int]struct{}, n)
	for idx := range tasks {
		remaining[idx] = struct{}{}
	}

	built := make([]int, 0, n) // lowest level first
	scratch := make([]int64, n)

	for level := 0; level < n; level++ {
		pending = n - level
		chosen := -1
		for idx := range remaining {
			hp := make([]int, 0, pending-1)
			for other := range remaining {
				if other != idx {
					hp = append(hp, other)
				}
			}
			_, r, ok := response(tasks, idx, hp, termCount)
			if !ok {
				continue
			}
			if chosen == -1 || tasks[idx].ID < tasks[chosen].ID {
				chosen = idx
			}
			scratch[idx] = r
		}
		if chosen == -1 {
			return nil, nil, pending
		}
		delete(remaining, chosen)
		built = append(built, chosen)
	}

	// built[0] holds the lowest-priority task; reverse for high-to-low.
	for i, j := 0, len(built)-1; i < j; i, j = i+1, j-1 {
		built[i], built[j] = built[j], built[i]
	}

	resp = make(map[int]int64, n)
	for pos, idx := range built {
		// Final cache must reflect the actual prefix in the produced order.
		// Feasibility is unchanged because the test depends only on the set.
		_, r, ok := response(tasks, idx, built[:pos], termCount)
		if !ok {
			panic("rta: audsley order became unschedulable")
		}
		resp[idx] = r
	}
	return built, resp, 0
}

// Add inserts a task incrementally, falling back to a full Audsley pass.
// reordered reports whether the whole set had to be reprioritized.
func (a *Analyzer) Add(t Task) (reordered bool, err error) {
	if err := t.valid(); err != nil {
		return false, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if _, exists := a.index[t.ID]; exists {
		return false, newError(ReasonDuplicate, 0, "rta: duplicate task id: "+t.ID)
	}
	if len(a.tasks) >= MaxTasks {
		return false, newError(ReasonCapacityFull, 0, "rta: task capacity reached")
	}

	// Candidate view: accepted tasks plus the new task at the end. Exact
	// capacity forces a fresh backing array so a rejected Add cannot touch
	// the live task records.
	cand := make([]Task, len(a.tasks)+1)
	copy(cand, a.tasks)
	cand[len(a.tasks)] = t
	newIdx := len(a.tasks)

	// Incremental insertion: try positions from the lowest level (end of
	// the order) upward; existing tasks keep their relative order. An empty
	// order offers exactly one position (p=0).
	for attempt := 0; attempt <= len(a.order); attempt++ {
		p := len(a.order) - attempt
		// Fresh backing array so the failed trials never overwrite order.
		trial := make([]int, 0, len(a.order)+1)
		trial = append(trial, a.order[:p]...)
		trial = append(trial, newIdx)
		trial = append(trial, a.order[p:]...)

		scratch := make([]int64, len(cand))
		// Tasks before p keep the same hp set: their cached R is unchanged
		// and need no recomputation.
		if recomputeFrom(cand, trial, p, scratch, &a.sumTerms) {
			a.tasks = cand
			a.order = trial
			a.resp[newIdx] = scratch[newIdx]
			for k := p; k < len(trial); k++ {
				idx := trial[k]
				if idx != newIdx {
					a.resp[idx] = scratch[idx]
				}
			}
			a.index[t.ID] = newIdx
			return false, nil
		}
	}

	// Every insertion point failed: rerun Audsley over the full candidate
	// set. State is published only on success, so a rejection leaves the
	// analyzer unchanged.
	newOrder, newResp, pending := audsley(cand, &a.sumTerms)
	if pending != 0 {
		return false, newError(ReasonUnschedulable, pending, "rta: task set unschedulable")
	}
	a.tasks = cand
	a.order = newOrder
	a.resp = newResp
	a.index[t.ID] = newIdx
	return true, nil
}

// Remove deletes a task preserving the relative order of the rest.
func (a *Analyzer) Remove(id string) error {
	if len(id) == 0 || len(id) > maxIDLen {
		return newError(ReasonInvalidParam, 0, "rta: task id must be non-empty and at most 64 bytes")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	idx, ok := a.index[id]
	if !ok {
		return newError(ReasonNotFound, 0, "rta: task not found: "+id)
	}

	pos := 0
	for a.order[pos] != idx {
		pos++
	}
	a.order = append(a.order[:pos], a.order[pos+1:]...)
	delete(a.index, id)
	delete(a.resp, idx)

	// Only survivors after the removed position need recomputation.
	scratch := make([]int64, len(a.tasks))
	if !recomputeFrom(a.tasks, a.order, pos, scratch, &a.sumTerms) {
		// Unreachable: removing a task cannot increase any interference sum.
		panic("rta: removal made task set unschedulable")
	}
	for k := pos; k < len(a.order); k++ {
		a.resp[a.order[k]] = scratch[a.order[k]]
	}
	return nil
}

// Order returns identifiers from highest priority to lowest.
func (a *Analyzer) Order() []string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]string, len(a.order))
	for pos, idx := range a.order {
		out[pos] = a.tasks[idx].ID
	}
	return out
}

// Response returns the cached response time of a task; it never iterates.
func (a *Analyzer) Response(id string) (int64, error) {
	if len(id) == 0 || len(id) > maxIDLen {
		return 0, newError(ReasonInvalidParam, 0, "rta: task id must be non-empty and at most 64 bytes")
	}

	a.mu.RLock()
	defer a.mu.RUnlock()
	idx, ok := a.index[id]
	if !ok {
		return 0, newError(ReasonNotFound, 0, "rta: task not found: "+id)
	}
	return a.resp[idx], nil
}

// SumTerms returns the total number of interference terms evaluated by
// fixed-point steps; used by tests to assert Response performs no iteration.
func (a *Analyzer) SumTerms() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sumTerms
}
