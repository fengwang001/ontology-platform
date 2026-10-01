package index

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Entry is one indexed row. Key holds one normalized value per column
// (nil for NULL); RowID breaks ties between equal keys so the index order
// is total and scans are deterministic.
type Entry struct {
	Key   []any
	RowID string
}

// Result is the outcome of one scan.
type Result struct {
	// Entries matches all conditions, in index order.
	Entries []Entry
	// Examined is the number of entries visited: exactly the entries of
	// the scanned snapshot that fall inside the derived intervals.
	Examined int
	// Plan is the derived access path.
	Plan *Plan
}

// Index is a multi-column ordered index safe for concurrent use. Writers
// copy-on-write; every scan observes one consistent snapshot taken at its
// start.
type Index struct {
	cols   []Column
	colIdx map[string]int

	mu   sync.Mutex // serializes writers
	snap atomic.Pointer[Snapshot]

	scans    atomic.Int64
	examined atomic.Int64
}

// Snapshot is an immutable, consistent view of the index. Scans over the
// same snapshot with the same conditions always return identical results.
type Snapshot struct {
	idx     *Index
	entries []Entry // sorted by (key, rowID)
	ids     map[string]struct{}
}

// NewIndex builds an empty index over the given columns.
func NewIndex(cols ...Column) *Index {
	idx := &Index{cols: cols, colIdx: make(map[string]int, len(cols))}
	for i, c := range cols {
		idx.colIdx[c.Name] = i
	}
	idx.snap.Store(&Snapshot{idx: idx, ids: map[string]struct{}{}})
	return idx
}

// Columns returns the column definitions.
func (idx *Index) Columns() []Column { return idx.cols }

// Stats reports the number of accepted scans and the total number of
// entries they examined. Rejected queries change neither counter.
func (idx *Index) Stats() (scans, examined int64) {
	return idx.scans.Load(), idx.examined.Load()
}

// Insert adds one row; values must match the column count and kinds
// (nil stores NULL). Duplicate RowIDs are rejected.
func (idx *Index) Insert(rowID string, values ...any) error {
	if len(values) != len(idx.cols) {
		return fmt.Errorf("index: got %d values for %d columns", len(values), len(idx.cols))
	}
	key := make([]any, len(values))
	for i, v := range values {
		if v == nil {
			continue
		}
		nv, err := normalizeValue(idx.cols[i].Kind, v)
		if err != nil {
			return fmt.Errorf("column %s: %w", idx.cols[i].Name, err)
		}
		key[i] = nv
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	prev := idx.snap.Load()
	old := prev.entries
	if _, dup := prev.ids[rowID]; dup {
		return fmt.Errorf("index: duplicate row id %q", rowID)
	}
	pos := sort.Search(len(old), func(i int) bool {
		return compareEntries(old[i], Entry{Key: key, RowID: rowID}) >= 0
	})
	next := make([]Entry, 0, len(old)+1)
	next = append(next, old[:pos]...)
	next = append(next, Entry{Key: key, RowID: rowID})
	next = append(next, old[pos:]...)
	ids := make(map[string]struct{}, len(prev.ids)+1)
	for id := range prev.ids {
		ids[id] = struct{}{}
	}
	ids[rowID] = struct{}{}
	idx.snap.Store(&Snapshot{idx: idx, entries: next, ids: ids})
	return nil
}

// Delete removes the row with the given id; it reports whether the row
// existed.
func (idx *Index) Delete(rowID string) bool {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	prev := idx.snap.Load()
	old := prev.entries
	if _, ok := prev.ids[rowID]; !ok {
		return false
	}
	pos := -1
	for i, e := range old {
		if e.RowID == rowID {
			pos = i
			break
		}
	}
	if pos < 0 {
		return false
	}
	next := make([]Entry, 0, len(old)-1)
	next = append(next, old[:pos]...)
	next = append(next, old[pos+1:]...)
	ids := make(map[string]struct{}, len(prev.ids)-1)
	for id := range prev.ids {
		if id != rowID {
			ids[id] = struct{}{}
		}
	}
	idx.snap.Store(&Snapshot{idx: idx, entries: next, ids: ids})
	return true
}

// Snapshot captures the current consistent view of the index.
func (idx *Index) Snapshot() *Snapshot { return idx.snap.Load() }

// Scan derives the access path for conds and scans one consistent snapshot
// taken at call time. comboLimit is the prefix combination cap L. Invalid
// queries are rejected as a whole with a distinguishable error and leave
// the statistics untouched.
func (idx *Index) Scan(conds []Condition, comboLimit int) (*Result, error) {
	plan, norm, err := derivePlan(idx.cols, conds, comboLimit)
	if err != nil {
		return nil, err
	}
	res := idx.snap.Load().scanPlan(plan, norm)
	idx.scans.Add(1)
	idx.examined.Add(int64(res.Examined))
	return res, nil
}

// Scan derives the access path and scans this snapshot. Repeated scans of
// the same snapshot with the same conditions return identical results.
func (s *Snapshot) Scan(conds []Condition, comboLimit int) (*Result, error) {
	plan, norm, err := derivePlan(s.idx.cols, conds, comboLimit)
	if err != nil {
		return nil, err
	}
	return s.scanPlan(plan, norm), nil
}

// Entries returns the snapshot's entries in index order (for tests and
// verification against full-table filtering).
func (s *Snapshot) Entries() []Entry { return s.entries }

// scanPlan visits exactly the entries inside the plan's intervals and
// applies every condition as the residual filter.
func (s *Snapshot) scanPlan(plan *Plan, conds []Condition) *Result {
	res := &Result{Plan: plan}
	if plan.Empty {
		return res
	}
	for _, iv := range plan.Intervals {
		lo := sort.Search(len(s.entries), func(i int) bool {
			c := cmpPrefix(s.entries[i].Key, iv.Lower.Prefix)
			return c > 0 || (c == 0 && iv.Lower.Inclusive)
		})
		hi := sort.Search(len(s.entries), func(i int) bool {
			c := cmpPrefix(s.entries[i].Key, iv.Upper.Prefix)
			return c > 0 || (c == 0 && !iv.Upper.Inclusive)
		})
		for _, e := range s.entries[lo:hi] {
			res.Examined++
			if matchesAll(s.idx.colIdx, conds, e.Key) {
				res.Entries = append(res.Entries, e)
			}
		}
	}
	return res
}

// matchesAll evaluates every condition against a key. Comparison operators
// never match NULL; only IS NULL does.
func matchesAll(colIdx map[string]int, conds []Condition, key []any) bool {
	for _, cond := range conds {
		v := key[colIdx[cond.Column]]
		switch cond.Op {
		case OpIsNull:
			if v != nil {
				return false
			}
		case OpEq:
			if v == nil || compareValues(v, cond.Value) != 0 {
				return false
			}
		case OpIn:
			if v == nil {
				return false
			}
			found := false
			for _, sv := range cond.Values {
				if compareValues(v, sv) == 0 {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		case OpLt, OpLe, OpGt, OpGe:
			if v == nil {
				return false
			}
			c := compareValues(v, cond.Value)
			ok := (cond.Op == OpLt && c < 0) || (cond.Op == OpLe && c <= 0) ||
				(cond.Op == OpGt && c > 0) || (cond.Op == OpGe && c >= 0)
			if !ok {
				return false
			}
		}
	}
	return true
}

func compareEntries(a, b Entry) int {
	for i := range a.Key {
		if c := compareValues(a.Key[i], b.Key[i]); c != 0 {
			return c
		}
	}
	switch {
	case a.RowID < b.RowID:
		return -1
	case a.RowID > b.RowID:
		return 1
	}
	return 0
}
