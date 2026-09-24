// Package mtree is one replica: in-memory map with incrementally maintained
// hashes over the F-ary interval tree, plus pairwise Diff.
package mtree

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/mhash"
)

// Range is a left-closed, right-open interval [Lo, Hi).
type Range struct{ Lo, Hi int64 }

// Op is one batch mutation: Del=true deletes Key (idempotent), else puts Val.
type Op struct {
	Del      bool
	Key, Val int64
}

var (
	ErrKeyRange = errors.New("mtree: key out of [0, N)")
	ErrShape    = errors.New("mtree: replicas have different F or D")
	ErrTooMany  = errors.New("mtree: key count exceeds limit")
)

var serial atomic.Uint64

// Tree is one replica; F, D, N are immutable after New.
type Tree struct {
	F, D, N int
	mu      sync.RWMutex
	id      uint64
	data    map[int64]int64
	hash    []int64 // node 0 = root; children of id: id*F+1 .. id*F+F
	cnt     []int32 // keys inside each node subtree
	reads   atomic.Int64
}

func New(f, d int) (*Tree, error) {
	if err := mhash.Validate(f, d, 1); err != nil {
		return nil, err
	}
	n := mhash.SpaceSize(f, d)
	nn := 1
	for w := 1; w < n; w *= f {
		nn += w * f
	}
	t := &Tree{F: f, D: d, N: n, id: serial.Add(1), data: map[int64]int64{}}
	t.hash, t.cnt = make([]int64, nn), make([]int32, nn)
	return t, nil
}

func (t *Tree) RootHash() int64 { t.mu.RLock(); defer t.mu.RUnlock(); return t.hash[0] }

func (t *Tree) Get(k int64) (int64, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	v, ok := t.data[k]
	return v, ok
}

// Batch validates every op and final size (<=limit) before any mutation, so a
// rejected batch leaves keys and hashes completely untouched.
func (t *Tree) Batch(ops []Op, limit int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	add := 0
	for _, o := range ops {
		if o.Key < 0 || o.Key >= int64(t.N) {
			return ErrKeyRange
		}
		if !o.Del {
			if _, ok := t.data[o.Key]; !ok {
				add++
			}
		}
	}
	if len(t.data)+add > limit {
		return ErrTooMany
	}
	for _, o := range ops {
		t.commit(o.Key, o.Val, o.Del)
	}
	return nil
}

// commit applies one validated op: bump counts up the path, then recompute
// ancestor hashes incrementally bottom-up.
func (t *Tree) commit(k, v int64, del bool) {
	id, _ := t.locate(k, 1)
	_, had := t.data[k]
	if del {
		if !had {
			return
		}
		delete(t.data, k)
		t.hash[id] = 0
		t.bump(id, -1)
	} else {
		if !had {
			t.bump(id, 1)
		}
		t.data[k] = v
		t.hash[id] = mhash.LeafHash(k, v)
	}
	t.refresh(id)
}

func (t *Tree) bump(leaf int, d int32) {
	for x := leaf; ; x = (x - 1) / t.F {
		t.cnt[x] += d
		if x == 0 {
			return
		}
	}
}

func (t *Tree) refresh(leaf int) {
	for x := leaf; x > 0; {
		p, b := (x-1)/t.F, 0
		b = p*t.F + 1
		if t.cnt[p] == 0 {
			t.hash[p] = 0 // rule 1: empty interval
		} else {
			t.hash[p] = mhash.Combine(t.hash[b : b+t.F])
		}
		x = p
	}
}

// locate finds the canonical node whose interval equals [lo, lo+width).
func (t *Tree) locate(lo, width int64) (int, bool) {
	if width <= 0 || lo < 0 || lo+width > int64(t.N) {
		return 0, false
	}
	id, origin, w := 0, int64(0), int64(t.N)
	for l := 0; l < t.D && w > width; l++ {
		step := w / int64(t.F)
		i := int((lo - origin) / step)
		id, origin, w = id*t.F+1+i, origin+int64(i)*step, step
	}
	return id, origin == lo && w == width
}
