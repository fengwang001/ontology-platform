// Package api is the public facade: writes, reads, replica Diff and SelfCheck.
package api

import (
	"errors"
	"fmt"

	"ontology/mhash"
	"ontology/mtree"
)

// Four distinct failure classes, decidable via errors.Is.
var (
	ErrParams   = errors.New("api: invalid parameters")
	ErrKeyRange = mtree.ErrKeyRange
	ErrShape    = mtree.ErrShape
	ErrTooMany  = mtree.ErrTooMany
)

type Range = mtree.Range
type Op struct {
	Del      bool
	Key, Val int64
}
type Replica struct {
	f, d, n, maxKeys int
	t                *mtree.Tree
}

// New validates F>=2, D>=1, F^D<=2^20, maxKeys>0 and builds a replica.
func New(f, d, maxKeys int) (*Replica, error) {
	if err := mhash.Validate(f, d, maxKeys); err != nil {
		return nil, ErrParams
	}
	t, _ := mtree.New(f, d) // Validate above guarantees success
	return &Replica{f, d, t.N, maxKeys, t}, nil
}

// Apply validates the whole batch first; any rejection changes nothing.
func (r *Replica) Apply(ops []Op) error {
	m := make([]mtree.Op, len(ops))
	for i, o := range ops {
		m[i] = mtree.Op{Del: o.Del, Key: o.Key, Val: o.Val}
	}
	return r.t.Batch(m, r.maxKeys)
}
func (r *Replica) Get(k int64) (int64, bool, error) {
	if k < 0 || k >= int64(r.n) {
		return 0, false, ErrKeyRange
	}
	v, ok := r.t.Get(k)
	return v, ok, nil
}
func (r *Replica) RootHash() int64 { return r.t.RootHash() }

// Diff returns differing keys (ascending, unique) and compared ranges.
func (r *Replica) Diff(o *Replica) ([]int64, []Range, error) {
	if r.f != o.f || r.d != o.d {
		return nil, nil, ErrShape
	}
	return r.t.Diff(o.t)
}

// naive recomputes [lo,hi) from scratch with rules 1-3 and reads only.
func (r *Replica) naive(lo, hi int64) (int64, bool) {
	if hi-lo == 1 {
		v, ok := r.t.Get(lo)
		if !ok {
			return 0, false
		}
		return mhash.LeafHash(lo, v), true
	}
	kids, any, w := make([]int64, r.f), false, hi-lo
	for i := range kids {
		h, ch := r.naive(lo+int64(i)*w/int64(r.f), lo+int64(i+1)*w/int64(r.f))
		kids[i], any = h, any || ch
	}
	if !any {
		return 0, false
	}
	return mhash.Combine(kids), true
}
func load(r *Replica, m map[int64]int64) {
	ops := make([]Op, 0, len(m))
	for k, v := range m {
		ops = append(ops, Op{Key: k, Val: v})
	}
	_ = r.Apply(ops)
}

// SelfCheck verifies the four invariants on built-in local replicas.
func (r *Replica) SelfCheck() error {
	a, _ := New(4, 3, 1000)
	b, _ := New(4, 3, 1000)
	av := map[int64]int64{1: 5, 2: 9, 4: 0, 9: 7, 13: 3, 63: 1 << 40}
	bv := map[int64]int64{1: 6, 2: 4, 9: 7, 13: 8, 41: 2, 63: 1 << 40}
	load(a, av)
	load(b, bv)
	keys, cmp, _ := a.Diff(b)
	if fmt.Sprint(keys) != "[1 2 4 13 41]" { // invariant 1
		return fmt.Errorf("invariant1: %v", keys)
	}
	for w := int64(1); w <= int64(a.n); w *= int64(a.f) { // invariant 2
		for lo := int64(0); lo < int64(a.n); lo += w {
			g, _ := a.t.Hash(lo, lo+w)
			h, _ := a.naive(lo, lo+w)
			if g != h {
				return fmt.Errorf("invariant2: [%d,%d) %d want %d", lo, lo+w, g, h)
			}
		}
	}
	seen := map[Range]bool{} // invariant 3
	for _, c := range cmp {
		seen[c] = true
	}
	for _, c := range cmp[1:] {
		pw, base := (c.Hi-c.Lo)*int64(a.f), c.Lo
		base -= base % pw
		ha, _ := a.t.Hash(base, base+pw)
		hb, _ := b.t.Hash(base, base+pw)
		if !seen[Range{Lo: base, Hi: base + pw}] || ha == hb {
			return fmt.Errorf("invariant3: %v", c)
		}
	}
	root := a.RootHash() // invariant 4
	c, _ := New(4, 3, 2)
	_ = c.Apply([]Op{{}, {Key: 1}})
	d, _ := New(8, 2, 10)
	_, _, shErr := a.Diff(d)
	_, prmErr := New(1, 1, 0)
	for _, cs := range []struct{ e, w error }{
		{prmErr, ErrParams},
		{a.Apply([]Op{{Key: -1, Val: 1}, {Key: 5, Val: 5}}), ErrKeyRange},
		{c.Apply([]Op{{Key: 2}}), ErrTooMany},
		{shErr, ErrShape},
	} {
		if !errors.Is(cs.e, cs.w) {
			return fmt.Errorf("invariant4: %v want %v", cs.e, cs.w)
		}
	}
	if a.RootHash() != root {
		return errors.New("invariant4: state changed after rejected ops")
	}
	if _, ok, _ := a.Get(5); ok {
		return errors.New("invariant4: rejected batch left a key")
	}
	return a.Apply([]Op{{Key: 5, Val: 5}}) // still usable afterwards
}
