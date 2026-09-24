package mtree

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/mhash"
)

func chk(t *testing.T, ok bool) {
	if !ok {
		t.FailNow()
	}
}
func nt(f, d int) *Tree {
	tr, _ := New(f, d)
	return tr
}
func seed(t *testing.T, tr *Tree, m map[int64]int64) {
	o := []Op{}
	for k, v := range m {
		o = append(o, Op{Key: k, Val: v})
	}
	chk(t, tr.Batch(o, 1<<20) == nil)
}
func rmap(n int64, c int, s int64) map[int64]int64 {
	r, m := rand.New(rand.NewSource(s)), map[int64]int64{}
	for i := 0; i < c; i++ {
		m[r.Int63n(n)] = r.Int63n(21) - 10
	}
	return m
}
func nHash(tr *Tree, lo, hi int64) int64 {
	id, _ := tr.locate(lo, hi-lo)
	if tr.cnt[id] == 0 {
		return 0
	}
	if hi-lo == 1 {
		v, _ := tr.Get(lo)
		return mhash.LeafHash(lo, v)
	}
	ks, w := []int64{}, hi-lo
	for i := 0; i < tr.F; i++ {
		ks = append(ks, nHash(tr, lo+int64(i)*w/int64(tr.F), lo+int64(i+1)*w/int64(tr.F)))
	}
	return mhash.Combine(ks)
}
func TestNewValidation(t *testing.T) {
	for _, x := range [][3]int{{1, 1, 1}, {2, 0, 1}, {2, 21, 1}, {2, 1, 0}, {3, 1, -1}} {
		chk(t, errors.Is(mhash.Validate(x[0], x[1], x[2]), mhash.ErrParams))
	}
}
func TestDiffNaiveParity(t *testing.T) {
	ok := true
	for _, c := range [][2]int{{2, 4}, {4, 3}, {2, 8}} {
		for s := int64(1); s <= 3; s++ {
			a, b := nt(c[0], c[1]), nt(c[0], c[1])
			n := int64(a.N)
			am, bm := rmap(n, int(n/3), s), rmap(n, int(n/3), s+100)
			seed(t, a, am)
			seed(t, b, bm)
			g, _, e := a.Diff(b)
			want := []int64{}
			for k := int64(0); k < n; k++ {
				v1, o1 := am[k]
				v2, o2 := bm[k]
				if o1 != o2 || o1 && v1 != v2 {
					want = append(want, k)
				}
			}
			ok = ok && e == nil && reflect.DeepEqual(g, want)
		}
	}
	chk(t, ok)
}
func TestIncrementalHashes(t *testing.T) {
	tr, r := nt(4, 4), rand.New(rand.NewSource(7))
	for i := 0; i < 120; i++ {
		op := Op{Key: r.Int63n(int64(tr.N)), Val: r.Int63n(101) - 50, Del: r.Intn(2) == 0}
		chk(t, tr.Batch([]Op{op}, 1<<20) == nil)
		chk(t, tr.hash[0] == nHash(tr, 0, int64(tr.N)))
	}
	ok := true
	for w := int64(1); w <= int64(tr.N); w *= int64(tr.F) {
		for lo := int64(0); lo < int64(tr.N); lo += w {
			x, _ := tr.locate(lo, w)
			ok = ok && tr.hash[x] == nHash(tr, lo, lo+w)
		}
	}
	chk(t, ok)
}
func TestDrillDiscipline(t *testing.T) {
	a, b := nt(4, 3), nt(4, 3)
	seed(t, a, rmap(64, 40, 7))
	seed(t, b, rmap(64, 40, 12))
	_, cmp, e := a.Diff(b)
	chk(t, e == nil)
	for _, c := range cmp[1:] {
		pw, x := (c.Hi-c.Lo)*4, c.Lo
		x -= x % pw
		ha, _ := a.Hash(x, x+pw)
		hb, _ := b.Hash(x, x+pw)
		chk(t, ha != hb)
	}
}
func TestRejectedAtomic(t *testing.T) {
	a, b := nt(2, 3), nt(4, 2)
	seed(t, a, map[int64]int64{0: 1, 1: 2})
	h0 := a.RootHash()
	ok := errors.Is(a.Batch([]Op{{Key: -1}, {Key: 2}}, 1<<20), ErrKeyRange)
	ok = ok && errors.Is(a.Batch([]Op{{Key: 2}}, 2), ErrTooMany)
	_, _, se := a.Diff(b)
	ok = ok && errors.Is(se, ErrShape)
	_, left := a.Get(2)
	ok = ok && a.RootHash() == h0 && !left
	ok = ok && mhash.ErrParams != ErrKeyRange && ErrKeyRange != ErrTooMany && ErrTooMany != ErrShape
	ok = ok && a.Batch([]Op{{Del: true, Key: 5}, {Key: 2, Val: 3}}, 1<<20) == nil
	chk(t, ok)
}
func TestDiffCostBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a, b := nt(4, 8), nt(4, 8)
		mm := rmap(65536, m, 1)
		seed(t, a, mm)
		seed(t, b, mm)
		chk(t, b.Batch([]Op{{Key: 61, Val: -1}}, 1<<20) == nil)
		_, _, e := a.Diff(b)
		chk(t, e == nil && a.lastReads() <= 4*(1+4*8))
	}
}
func TestConcurrentDiffIdentical(t *testing.T) {
	a, b := nt(4, 4), nt(4, 4)
	seed(t, a, rmap(256, 200, 3))
	seed(t, b, rmap(256, 200, 3))
	chk(t, b.Batch([]Op{{Key: 7, Val: -7}, {Del: true, Key: 11}}, 1<<20) == nil)
	var wg sync.WaitGroup
	res := make([]string, 12)
	for g := range res {
		wg.Add(1)
		go func(g int) { defer wg.Done(); ks, c, e := a.Diff(b); res[g] = fmt.Sprint(ks, c, e) }(g)
	}
	wg.Wait()
	for _, s := range res[1:] {
		chk(t, s == res[0])
	}
}
