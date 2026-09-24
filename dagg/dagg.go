// Package dagg applies batched inserts/withdrawals over groups and emits a
// per-batch changelog of distinct-count deltas.
package dagg

import (
	"errors"
	"sort"
	"sync"

	"ontology/mset"
)

// Change is one ordered mutation: Sign +1 inserts once, -1 withdraws once.
type Change struct {
	Group, Val string
	Sign       int
}

// C builds a Change with keyed fields (keeps go vet quiet at call sites).
func C(group, val string, sign int) Change { return Change{Group: group, Val: val, Sign: sign} }

// Out is one changelog entry: Sign +1 sets Group's visible value to N, -1 withdraws it.
type Out struct {
	Sign  int
	Group string
	N     int
}

// Sentinel errors; the three failure classes are always distinct.
var (
	ErrWithdrawAbsent = errors.New("dagg: withdrawal of an absent value")
	ErrInvalidChange  = errors.New("dagg: invalid change (sign must be ±1, group/val non-empty)")
	ErrLimitExceeded  = errors.New("dagg: global live entry count exceeds maxEntries")
)

// Agg holds all groups, the emitted changelog and resource limits.
type Agg struct {
	mu      sync.RWMutex
	groups  map[string]*mset.M
	log     []Out
	max     int
	total   int // global live (Group,Val) entries, maintained incrementally
	checked int // entries inspected for distinct changes in the latest Feed
}

// New creates an Agg capped at maxEntries live (Group,Val) entries.
func New(maxEntries int) *Agg { return &Agg{groups: map[string]*mset.M{}, max: maxEntries} }

// Feed validates and applies one batch atomically against the in-batch ordered
// state; on rejection nothing changes. Returns this batch's entries only.
func (a *Agg) Feed(batch []Change) ([]Out, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.checked = 0
	for _, c := range batch {
		if (c.Sign != 1 && c.Sign != -1) || c.Group == "" || c.Val == "" {
			return nil, ErrInvalidChange
		}
	}
	journal := make([]Change, 0, len(batch))
	created, old := map[string]bool{}, map[string]int{}
	rollback := func() { // reverse the applied prefix, then drop new groups
		for i := len(journal) - 1; i >= 0; i-- {
			c, s := journal[i], a.groups[journal[i].Group]
			if c.Sign == 1 {
				if down, ok := s.Remove(c.Val); ok && down {
					a.total--
				}
			} else if s.Add(c.Val) {
				a.total++
			}
		}
		for g := range created {
			delete(a.groups, g)
		}
		a.checked = 0
	}
	for _, c := range batch {
		a.checked++
		s := a.groups[c.Group]
		if s == nil {
			s = mset.New()
			a.groups[c.Group] = s
			created[c.Group] = true
		}
		if _, t := old[c.Group]; !t {
			old[c.Group] = s.Distinct()
		}
		switch c.Sign {
		case 1:
			if s.Add(c.Val) {
				a.total++
			}
		case -1:
			down, ok := s.Remove(c.Val) // checked against in-batch state
			if !ok {
				rollback()
				return nil, ErrWithdrawAbsent
			}
			if down {
				a.total--
			}
		}
		journal = append(journal, c)
		if a.total > a.max {
			rollback()
			return nil, ErrLimitExceeded
		}
	}
	touched := make([]string, 0, len(old))
	for g := range old {
		touched = append(touched, g)
	}
	sort.Strings(touched)
	out := make([]Out, 0)
	for _, g := range touched { // groups sorted; each group's pair is adjacent
		o, n := old[g], a.groups[g].Distinct()
		if o == n {
			continue
		}
		if o > 0 {
			out = append(out, Out{-1, g, o})
		}
		if n > 0 {
			out = append(out, Out{1, g, n})
		}
	}
	a.log = append(a.log, out...)
	return out, nil
}

// View returns a snapshot group -> distinct count, omitting zero groups.
func (a *Agg) View() map[string]int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	v := make(map[string]int, len(a.groups))
	for g, s := range a.groups {
		if d := s.Distinct(); d > 0 {
			v[g] = d
		}
	}
	return v
}

// Log returns a copy of every entry emitted by accepted batches.
func (a *Agg) Log() []Out {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return append([]Out(nil), a.log...)
}
