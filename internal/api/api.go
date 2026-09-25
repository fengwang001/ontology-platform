// Package api is the public facade of the in-process 2-3 tree. It depends
// only on internal/btree, which owns the synchronization.
package api

import (
	"errors"
	"slices"
	"sort"

	"ontology/internal/btree"
)

// Distinct decidable sentinel errors (section 5).
var (
	ErrDuplicateKey = btree.ErrDuplicateKey
	ErrKeyNotFound  = btree.ErrKeyNotFound
	ErrTreeFull     = btree.ErrTreeFull
	ErrInvalidMax   = btree.ErrInvalidMax
	ErrSelfCheck    = errors.New("api: self-check found a broken invariant")
)

// Tree is safe for concurrent readers and writers.
type Tree struct{ t *btree.Tree }

// New creates an empty tree holding at most maxKeys keys.
func New(maxKeys int) (*Tree, error) {
	t, err := btree.New(maxKeys)
	if err != nil {
		return nil, err
	}
	return &Tree{t: t}, nil
}

func (x *Tree) Insert(k int) (int, error) { return x.t.Insert(k) }
func (x *Tree) Delete(k int) (int, error) { return x.t.Delete(k) }
func (x *Tree) Search(k int) bool         { return x.t.Search(k) }
func (x *Tree) OrderedKeys() []int        { return x.t.OrderedKeys() }
func (x *Tree) Height() int               { return x.t.Height() }

// SelfCheck verifies the four invariants on the receiver and replays the
// built-in section-3 operation sequence on a fresh tree.
func (x *Tree) SelfCheck() error {
	if err := checkLive(x.t); err != nil {
		return err
	}
	return checkSequence()
}

// checkLive verifies structure (inv.1), strictly ascending keys == size
// (inv.2) and Search == binary search for present and absent keys (inv.3).
func checkLive(t *btree.Tree) error {
	if !t.Valid() {
		return ErrSelfCheck
	}
	ks := t.OrderedKeys()
	for i := 1; i < len(ks); i++ {
		if ks[i-1] >= ks[i] {
			return ErrSelfCheck
		}
	}
	for _, k := range ks {
		if !t.Search(k) {
			return ErrSelfCheck
		}
	}
	for _, k := range absentProbes(ks) {
		i := sort.SearchInts(ks, k)
		if t.Search(k) != (i < len(ks) && ks[i] == k) {
			return ErrSelfCheck
		}
	}
	return nil
}

// absentProbes returns deterministic keys guaranteed not in ks: boundaries
// and the first interior gaps.
func absentProbes(ks []int) []int {
	if len(ks) == 0 {
		return []int{-1, 0, 1}
	}
	have := map[int]bool{}
	for _, k := range ks {
		have[k] = true
	}
	out := []int{ks[0] - 1, ks[len(ks)-1] + 1}
	for i := 0; i+1 < len(ks) && len(out) < 8; i++ {
		if ks[i+1]-ks[i] > 1 && !have[ks[i]+1] {
			out = append(out, ks[i]+1)
		}
	}
	return out
}

// checkSequence replays the section-3 sequence: seven ascending inserts with
// per-step cost/root, then Delete(3) with its two merges; then it confirms the
// rejected calls leave no trace (inv.4).
func checkSequence() error {
	t, _ := btree.New(1000)
	costs := []int{0, 0, 1, 0, 1, 0, 2}
	roots := [][]int{{1}, {1, 2}, {2}, {2}, {2, 4}, {2, 4}, {4}}
	for i, k := range []int{1, 2, 3, 4, 5, 6, 7} {
		c, err := t.Insert(k)
		r, _ := t.Snapshot()
		if err != nil || c != costs[i] || !slices.Equal(r, roots[i]) || !t.Valid() {
			return ErrSelfCheck
		}
	}
	if !slices.Equal(t.OrderedKeys(), []int{1, 2, 3, 4, 5, 6, 7}) {
		return ErrSelfCheck
	}
	c, err := t.Delete(3) // two merges -> root [4,6], leaves [1,2] [5] [7]
	r, lv := t.Snapshot()
	if err != nil || c != 2 || !slices.Equal(r, []int{4, 6}) || !slices.Equal(lv[0], []int{1, 2}) ||
		!slices.Equal(lv[1], []int{5}) || !slices.Equal(lv[2], []int{7}) || !t.Valid() {
		return ErrSelfCheck
	}
	if _, e := t.Insert(1); !errors.Is(e, btree.ErrDuplicateKey) {
		return ErrSelfCheck
	}
	if _, e := t.Delete(99); !errors.Is(e, btree.ErrKeyNotFound) || t.Size() != 6 {
		return ErrSelfCheck
	}
	return nil
}
