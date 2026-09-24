// Package api is the public facade (New/Apply/Get/RootHash/Diff/SelfCheck); it imports only mtree, so dependencies stay one-way.
package api

import (
	"errors"
	"maps"
	"math/rand"
	"reflect"
	"slices"

	"ontology/mhash"
	"ontology/mtree"
)

type (
	Op      = mtree.Op
	Range   = mtree.Range
	Replica struct {
		t       *mtree.Tree
		maxKeys int
	}
)

var ( // five decidable sentinels; the four spec failure classes are distinct
	ErrInvalidParams  = mhash.ErrInvalidParams // F<2, D<1 or F^D>2^20
	ErrInvalidMaxKeys = errors.New("api: maxKeys must be positive")
	ErrKeyOutOfRange  = mtree.ErrKeyOutOfRange // k<0 or k>=N
	ErrMaxKeys        = mtree.ErrMaxKeys       // batch would exceed maxKeys
	ErrShapeMismatch  = mtree.ErrShapeMismatch // Diff across different F/D
)

func Put(k, v int64) Op { return mtree.Put(k, v) }
func Delete(k int64) Op { return mtree.Delete(k) }
func New(F, D, maxKeys int) (*Replica, error) {
	p, err := mhash.NewParams(F, D)
	if maxKeys <= 0 && err == nil {
		err = ErrInvalidMaxKeys
	}
	if err != nil {
		return nil, err
	}
	return &Replica{t: mtree.New(p), maxKeys: maxKeys}, nil
}
func (r *Replica) Apply(ops []Op) error                      { return r.t.ApplyOps(ops, r.maxKeys) }
func (r *Replica) Get(k int64) (int64, bool)                 { return r.t.Get(k) }
func (r *Replica) RootHash() int64                           { return r.t.RootHash() }
func (r *Replica) Diff(o *Replica) ([]int64, []Range, error) { return r.t.Diff(o.t) }
func naiveKeys(a, b map[int64]int64) []int64 { // per-key presence/value reference
	set := map[int64]bool{}
	for k := range a {
		if v, ok := b[k]; !ok || v != a[k] {
			set[k] = true
		}
	}
	for k := range b {
		if v, ok := a[k]; !ok || v != b[k] {
			set[k] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}
func recompute(t *mtree.Tree) bool { // rehash all nodes via Get/Hash (invariant 2)
	p := t.Params()
	f := int64(p.F)
	cur := make([]int64, p.N)
	for k := range p.N {
		if v, ok := t.Get(k); ok {
			cur[k] = mhash.LeafHash(k, v)
		}
	}
	for w := int64(1); w < p.N; w *= f { // verify this level, then fold parents
		for j, h := range cur {
			if t.Hash(int64(j)*w, int64(j+1)*w) != h {
				return false
			}
		}
		nxt := make([]int64, len(cur)/p.F)
		for j := range nxt {
			nxt[j] = mhash.CombineNode(cur[int64(j)*f : int64(j+1)*f])
		}
		cur = nxt
	}
	return t.RootHash() == cur[0]
}
func parentsOK(a, b *mtree.Tree, seq []mtree.Range) bool { // invariant 3
	in, neq := map[mtree.Range]bool{}, map[mtree.Range]bool{}
	for _, x := range seq {
		in[x] = true
		neq[x] = a.Hash(x.Lo, x.Hi) != b.Hash(x.Lo, x.Hi)
	}
	for _, x := range seq[1:] {
		pw := (x.Hi - x.Lo) * int64(a.Params().F)
		lo := x.Lo - x.Lo%pw
		par := mtree.Range{Lo: lo, Hi: lo + pw}
		if !in[par] || !neq[par] {
			return false
		}
	}
	return true
}
func randData(rng *rand.Rand, n int64) (map[int64]int64, []mtree.Op) {
	m, ops := map[int64]int64{}, []mtree.Op{}
	for range 40 {
		if rng.Intn(5) != 0 {
			k, v := rng.Int63n(n), rng.Int63n(21)-5
			m[k], ops = v, append(ops, mtree.Put(k, v))
		}
	}
	return m, ops
}
func otherShape(p mhash.Params) mhash.Params { // a valid shape differing from p
	if q, e := mhash.NewParams(p.F, p.D+1); e == nil {
		return q
	}
	q, _ := mhash.NewParams(p.F+1, 1)
	return q
}
func (r *Replica) SelfCheck() error { // four invariants on built-in data; r untouched
	p, rng := r.t.Params(), rand.New(rand.NewSource(1))
	for range 4 { // invariants 1-3 on random replicas
		am, ao := randData(rng, p.N)
		bm, bo := randData(rng, p.N)
		a, b := mtree.New(p), mtree.New(p)
		if err := a.ApplyOps(ao, 0); err != nil {
			return err
		} else if err := b.ApplyOps(bo, 0); err != nil {
			return err
		}
		keys, seq, err := a.Diff(b)
		if err != nil || !reflect.DeepEqual(keys, naiveKeys(am, bm)) ||
			!recompute(a) || !recompute(b) || !parentsOK(a, b, seq) {
			return errors.New("selfcheck: invariants 1-3")
		}
	}
	c := mtree.New(p) // invariant 4: rejected batches leave no trace
	if err := c.ApplyOps([]mtree.Op{mtree.Put(0, 1), mtree.Put(1, 2)}, 2); err != nil {
		return err
	}
	before := c.RootHash()
	check := func(op mtree.Op, want error) bool {
		return errors.Is(c.ApplyOps([]mtree.Op{op}, 2), want) && c.RootHash() == before && recompute(c)
	}
	if !check(mtree.Put(p.N, 1), ErrKeyOutOfRange) || !check(mtree.Put(2, 1), ErrMaxKeys) {
		return errors.New("selfcheck: invariant 4")
	}
	if _, _, e := c.Diff(mtree.New(otherShape(p))); !errors.Is(e, ErrShapeMismatch) {
		return errors.New("selfcheck: shape error")
	}
	return nil
}
