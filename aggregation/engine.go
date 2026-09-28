package aggregation

import (
	"fmt"
	"sync"
)

// Engine is the global phase. It accepts only partial aggregates produced
// by the local phase and merges them under a mutex, so Submit and Snapshot
// are safe for concurrent use by multiple executors. Sum and count merge
// by plain addition; the average is derived as merged sum / merged count;
// the distinct count is maintained by pushing value-level row counts down
// into the global state. Groups whose count drops to zero are deleted and
// may be recreated by later batches.
type Engine struct {
	mu        sync.RWMutex
	maxGroups int
	groups    map[string]*groupState
	accepted  int64
}

type groupState struct {
	sum    float64
	count  int64
	values map[float64]int64
}

// NewEngine creates a global engine accepting at most maxGroups live groups.
func NewEngine(maxGroups int) *Engine {
	return &Engine{maxGroups: maxGroups, groups: make(map[string]*groupState)}
}

// Submit validates and atomically merges one batch of partial aggregates.
// The whole batch is checked before anything is applied: on any illegal
// input the batch is rejected with a distinguishable reason and neither
// the global state nor the accepted-batch count changes.
func (e *Engine) Submit(partials map[string]PartialAgg) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	live := len(e.groups)
	for group, p := range partials {
		if group == "" {
			return &Error{Code: ErrCodeEmptyGroup, Msg: "partial aggregate with empty group name"}
		}
		st := e.groups[group]
		if st == nil {
			if p.Count < 0 {
				return &Error{Code: ErrCodeRetractMissingRow,
					Msg: fmt.Sprintf("group %q: retract of %d rows that do not exist", group, -p.Count)}
			}
			for v, delta := range p.Values {
				if delta < 0 {
					return &Error{Code: ErrCodeRetractMissingRow,
						Msg: fmt.Sprintf("group %q: retract of absent value %v", group, v)}
				}
			}
			if p.Count > 0 {
				live++
			}
			continue
		}
		merged := st.count + p.Count
		if merged < 0 {
			return &Error{Code: ErrCodeRetractMissingRow,
				Msg: fmt.Sprintf("group %q: retract of %d rows exceeds %d existing", group, -p.Count, st.count)}
		}
		for v, delta := range p.Values {
			if st.values[v]+delta < 0 {
				return &Error{Code: ErrCodeRetractMissingRow,
					Msg: fmt.Sprintf("group %q: retract of value %v exceeds existing rows", group, v)}
			}
		}
		if merged == 0 {
			live--
		}
	}
	if live > e.maxGroups {
		return &Error{Code: ErrCodeTooManyGroups,
			Msg: fmt.Sprintf("merge would leave %d groups, limit is %d", live, e.maxGroups)}
	}

	for group, p := range partials {
		st := e.groups[group]
		if st == nil {
			st = &groupState{values: make(map[float64]int64)}
			e.groups[group] = st
		}
		st.sum += p.Sum
		st.count += p.Count
		for v, delta := range p.Values {
			st.values[v] += delta
			if st.values[v] == 0 {
				delete(st.values, v)
			}
		}
		if st.count == 0 {
			delete(e.groups, group)
		}
	}
	e.accepted++
	return nil
}

// Snapshot returns a consistent copy of the four indicators per group.
// The average is computed only here, from the already merged sum and count.
func (e *Engine) Snapshot() map[string]Metrics {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make(map[string]Metrics, len(e.groups))
	for group, st := range e.groups {
		m := Metrics{
			Sum:          st.sum,
			Count:        st.count,
			DistinctVals: int64(len(st.values)),
		}
		if st.count > 0 {
			m.Avg = st.sum / float64(st.count)
		}
		out[group] = m
	}
	return out
}

// AcceptedBatches reports how many batches have been accepted so far.
func (e *Engine) AcceptedBatches() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.accepted
}
