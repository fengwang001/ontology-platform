package mtree

import "ontology/mhash"

// Hash returns the maintained hash of the canonical interval [lo, hi).
func (t *Tree) Hash(lo, hi int64) (int64, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	id, ok := t.locate(lo, hi-lo)
	if !ok {
		return 0, false
	}
	return t.hash[id], true
}

// lastReads exposes the unexported Diff cost counter (interval hashes read +
// leaf keys visited by the most recent Diff) to in-package tests only; the
// counter is absent from the exported API.
func (t *Tree) lastReads() int64 { return t.reads.Load() }

// Diff returns differing keys (ascending, unique) and compared ranges (DFS,
// children ascending). Locks are taken in id order so concurrent a.Diff(b)
// and b.Diff(a) cannot deadlock.
func (t *Tree) Diff(o *Tree) ([]int64, []Range, error) {
	if t.F != o.F || t.D != o.D {
		return nil, nil, ErrShape
	}
	first, second := t, o
	if t.id > o.id {
		first, second = o, t
	}
	first.mu.RLock()
	defer first.mu.RUnlock()
	second.mu.RLock()
	defer second.mu.RUnlock()
	keys, cmp, reads := []int64{}, []Range{}, 0
	var dfs func(id int, lo, hi int64)
	dfs = func(id int, lo, hi int64) {
		reads += 2 // one interval hash read per replica
		cmp = append(cmp, Range{lo, hi})
		if t.hash[id] == o.hash[id] {
			return
		}
		if hi-lo == 1 {
			keys = append(keys, lo)
			reads++ // the differing leaf key is visited
			return
		}
		for i := 0; i < t.F; i++ {
			clo, chi := mhash.ChildRange(lo, hi-lo, t.F, i)
			dfs(id*t.F+1+i, clo, chi)
		}
	}
	dfs(0, 0, int64(t.N))
	t.reads.Store(int64(reads))
	return keys, cmp, nil
}
