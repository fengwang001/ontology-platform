// Package prune holds the ordered partition table and runs static then
// dynamic pruning. It depends only on part.
package prune

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/part"
)

var (
	// ErrInvalidPredicate: PLo >= PHi or VLo >= VHi.
	ErrInvalidPredicate = errors.New("prune: invalid predicate: PLo >= PHi or VLo >= VHi")
	// ErrUnknownPartition: a result references an unregistered partition id.
	ErrUnknownPartition = errors.New("prune: unknown partition id referenced")
	errComplexity       = errors.New("prune: checked-partition complexity check failed")
)

// Predicate is the conjunction p in [PLo,PHi) AND v in [VLo,VHi).
type Predicate struct {
	PLo, PHi, VLo, VHi int64
}

func (q Predicate) valid() bool { return q.PLo < q.PHi && q.VLo < q.VHi }

// Result is the surviving set (Scan) and removed set (Pruned), in lo order.
type Result struct {
	Scan   []string
	Pruned []string
}

// eval is one query run: Result plus the unexported checked counter; it is
// per-call (concurrency-safe) and Prune exposes only its Result half.
type eval struct {
	Result
	checked int
}

// Table is a set of partitions ordered by lo and mutually non-overlapping.
type Table struct {
	mu    sync.RWMutex
	parts []part.Part
	byID  map[string]part.Part
}

func NewTable() *Table { return &Table{byID: map[string]part.Part{}} }

// Add inserts in lo order; checks precede mutation, so rejection is a no-op.
func (t *Table) Add(p part.Part) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.byID[p.ID()]; exists {
		return part.ErrInvalidPartition
	}
	i := sort.Search(len(t.parts), func(k int) bool { return t.parts[k].Lo() >= p.Lo() })
	if i > 0 && t.parts[i-1].Hi() > p.Lo() {
		return part.ErrInvalidPartition
	}
	if i < len(t.parts) && p.Hi() > t.parts[i].Lo() {
		return part.ErrInvalidPartition
	}
	t.parts = append(t.parts, part.Part{})
	copy(t.parts[i+1:], t.parts[i:])
	t.parts[i] = p
	t.byID[p.ID()] = p
	return nil
}

// IDs returns all registered ids in ascending lo order.
func (t *Table) IDs() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ids := make([]string, len(t.parts))
	for i, p := range t.parts {
		ids[i] = p.ID()
	}
	return ids
}

// Prune static-prunes on p via a binary-search window, then dynamic-prunes on
// v inside it. All ids are resolved first, so an unknown id fails atomically.
func (t *Table) Prune(ids []string, q Predicate) (Result, error) {
	r, err := t.evaluate(ids, q)
	return r.Result, err
}

// evaluate runs one query, returning eval; only its Result is exported.
func (t *Table) evaluate(ids []string, q Predicate) (eval, error) {
	if !q.valid() {
		return eval{}, ErrInvalidPredicate
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	chosen := make([]part.Part, 0, len(ids))
	for _, id := range ids {
		p, ok := t.byID[id]
		if !ok {
			return eval{}, ErrUnknownPartition
		}
		chosen = append(chosen, p)
	}
	sort.Slice(chosen, func(a, b int) bool { return chosen[a].Lo() < chosen[b].Lo() })
	r := eval{}
	// [a,b) is the p-intersecting window via binary search. Only its members
	// get a box comparison (and increment checked); outside members are
	// classified by index. checked is therefore constant for a narrow query.
	a := sort.Search(len(chosen), func(k int) bool { return chosen[k].Hi() > q.PLo })
	b := sort.Search(len(chosen), func(k int) bool { return chosen[k].Lo() >= q.PHi })
	for k, p := range chosen {
		if k < a || k >= b {
			r.Pruned = append(r.Pruned, p.ID())
			continue
		}
		r.checked++
		if p.DynamicPruned(q.VLo, q.VHi) {
			r.Pruned = append(r.Pruned, p.ID())
		} else {
			r.Scan = append(r.Scan, p.ID())
		}
	}
	return r, nil
}

// CheckComplexity verifies via the unexported counter that checked does not
// grow with N; only pass/fail crosses the package boundary.
func CheckComplexity() error {
	for _, n := range []int{100, 1000, 10000} {
		t := NewTable()
		for i := 0; i < n; i++ {
			p, err := part.New(fmt.Sprintf("P%05d", i), int64(i*10), int64(i*10+10), 0, 100)
			if err != nil {
				return err
			}
			if err := t.Add(p); err != nil {
				return err
			}
		}
		e, err := t.evaluate(t.IDs(), Predicate{PLo: 15, PHi: 16, VLo: 0, VHi: 1000})
		if err != nil {
			return err
		}
		if e.checked > 3 || len(e.Scan) != 1 { // constant + truly-in-window (1)
			return errComplexity
		}
	}
	return nil
}
