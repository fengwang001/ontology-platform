// Package mtree owns one replica: storage, incrementally maintained range hashes and two-replica Diff. Depends only on mhash.
package mtree

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/mhash"
)

// Failure sentinels; with mhash.ErrInvalidParams the four failure classes are mutually distinct.
var (
	ErrKeyOutOfRange = errors.New("mtree: key out of range")          // k<0 or k>=N
	ErrMaxKeys       = errors.New("mtree: key count exceeds maxKeys") // batch over cap
	ErrShapeMismatch = errors.New("mtree: replicas have different shape")
)

type Range struct{ Lo, Hi int64 } // [Lo, Hi)
type Op struct {
	K, V int64
	Del  bool
}

func Put(k, v int64) Op { return Op{K: k, V: v} }
func Delete(k int64) Op { return Op{K: k, Del: true} }

// Tree is one replica: levels[d][j] is node j at depth d (0=root, D=leaf j on [j,j+1)); empty nodes stay zero. cnt is the unexported counter: range hashes read + keys visited in the latest Diff on this receiver.
type Tree struct {
	p      mhash.Params
	mu     sync.RWMutex
	data   map[int64]int64
	levels [][]int64
	cnt    atomic.Uint64
}

func New(p mhash.Params) *Tree {
	levels := make([][]int64, p.D+1)
	for d, w := p.D, int64(1); d >= 0; d, w = d-1, w*int64(p.F) {
		levels[d] = make([]int64, p.N/w)
	}
	return &Tree{p: p, data: map[int64]int64{}, levels: levels}
}
func (t *Tree) Params() mhash.Params { return t.p }
func (t *Tree) Get(k int64) (v int64, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	v, ok = t.data[k]
	return
}
func (t *Tree) Hash(lo, hi int64) int64 { // maintained hash of aligned node [lo,hi)
	w, d := hi-lo, t.p.D-t.p.DepthOf(hi-lo) // level 0=root(width N), D=leaves
	if d < 0 || d > t.p.D || lo%w != 0 || w <= 0 {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.levels[d][lo/w]
}
func (t *Tree) RootHash() int64 { t.mu.RLock(); defer t.mu.RUnlock(); return t.levels[0][0] }
func (t *Tree) recomputeLocked(d int, j int64) { // rule 1 (empty subtree -> 0), else rule 3
	base := j * int64(t.p.F)
	t.levels[d][j] = mhash.CombineNode(t.levels[d+1][base : base+int64(t.p.F)])
}
func (t *Tree) setPathLocked(k int64) { // update only the leaf-to-root path of k
	t.levels[t.p.D][k] = 0
	if v, ok := t.data[k]; ok {
		t.levels[t.p.D][k] = mhash.LeafHash(k, v)
	}
	for d, j := t.p.D-1, k/int64(t.p.F); d >= 0; d, j = d-1, j/int64(t.p.F) {
		t.recomputeLocked(d, j)
	}
}
func (t *Tree) ApplyOps(ops []Op, maxKeys int) error { // validate whole batch first; rejection atomic
	for _, o := range ops {
		if o.K < 0 || o.K >= t.p.N {
			return ErrKeyOutOfRange
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if maxKeys > 0 { // final presence per touched key, last op wins
		fin := map[int64]bool{}
		for _, o := range ops {
			fin[o.K] = !o.Del
		}
		n := len(t.data)
		for k, pres := range fin {
			_, ex := t.data[k]
			if pres && !ex {
				n++
			}
			if !pres && ex {
				n--
			}
		}
		if n > maxKeys {
			return ErrMaxKeys
		}
	}
	for _, o := range ops {
		if _, ok := t.data[o.K]; o.Del && ok {
			delete(t.data, o.K)
			t.setPathLocked(o.K)
		} else if !o.Del {
			t.data[o.K] = o.V // insert/overwrite; only this key's path changes
			t.setPathLocked(o.K)
		}
	}
	return nil
}
func (t *Tree) Diff(other *Tree) (keys []int64, seq []Range, err error) { // DFS, children ascending
	if t.p.F != other.p.F || t.p.D != other.p.D {
		return nil, nil, ErrShapeMismatch
	}
	// Both locks are read locks: they never block each other, and writes never
	// span two trees, so no lock-ordering rule is needed to avoid deadlock.
	t.mu.RLock()
	defer t.mu.RUnlock()
	other.mu.RLock()
	defer other.mu.RUnlock()
	t.cnt.Store(0)
	var dfs func(d int, j, lo, hi int64)
	dfs = func(d int, j, lo, hi int64) {
		ha, hb := t.levels[d][j], other.levels[d][j]
		t.cnt.Add(2) // two maintained range-hash reads; no stored key visited
		seq = append(seq, Range{lo, hi})
		if ha == hb {
			return
		}
		if d == t.p.D {
			keys = append(keys, lo)
			return
		}
		for i := 0; i < t.p.F; i++ {
			clo, chi := t.p.Child(lo, hi, i)
			dfs(d+1, j*int64(t.p.F)+int64(i), clo, chi)
		}
	}
	dfs(0, 0, 0, t.p.N)
	return keys, seq, nil
}
