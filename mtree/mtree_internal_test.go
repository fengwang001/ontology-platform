package mtree

import (
	"math/rand"
	"reflect"
	"testing"

	"ontology/mhash"
)

func chk(err error) {
	if err != nil {
		panic(err)
	}
}
func naiveHash(m map[int64]int64, F int, lo, hi int64) int64 { // three rules from scratch
	any := false
	for k := lo; k < hi; k++ {
		if _, ok := m[k]; ok {
			any = true
		}
	}
	if !any {
		return 0 // rule 1
	}
	if hi-lo == 1 {
		return mhash.LeafHash(lo, m[lo]) // rule 2
	}
	w, f := hi-lo, int64(F)
	kids := make([]int64, F)
	for i := range kids {
		kids[i] = naiveHash(m, F, lo+int64(i)*w/f, lo+int64(i+1)*w/f)
	}
	return mhash.Combine(kids) // rule 3
}
func fillTree(p mhash.Params, seed int64, batches int) (*Tree, map[int64]int64) {
	tr, final, rng := New(p), map[int64]int64{}, rand.New(rand.NewSource(seed))
	for range batches {
		var ops []Op
		for range 15 {
			k, v := rng.Int63n(p.N), rng.Int63n(21)-7
			if rng.Intn(4) == 0 {
				ops = append(ops, Delete(k))
				delete(final, k)
			} else {
				ops = append(ops, Put(k, v))
				final[k] = v
			}
		}
		chk(tr.ApplyOps(ops, 0))
	}
	return tr, final
}
func TestSection3HashTable(t *testing.T) {
	p, _ := mhash.NewParams(4, 2)
	a, b := New(p), New(p)
	chk(a.ApplyOps([]Op{Put(1, 5), Put(2, 5), Put(4, 0), Put(9, 7), Put(13, 3)}, 0))
	chk(b.ApplyOps([]Op{Put(1, 6), Put(2, 4), Put(9, 7), Put(13, 8)}, 0))
	want := [][4]int64{ // lo, hi, hashA, hashB: all 17 compared rows
		{0, 16, 322749291, 477521251}, {0, 4, 834376390, 989159576},
		{0, 1, 0, 0}, {1, 2, 1000165, 1000196}, {2, 3, 2000168, 2000137}, {3, 4, 0, 0},
		{4, 8, 477635560, 0}, {4, 5, 4000019, 0}, {5, 6, 0, 0}, {6, 7, 0, 0}, {7, 8, 0, 0},
		{8, 12, 247453045, 247453045},
		{12, 16, 612068240, 540984628}, {12, 13, 0, 0}, {13, 14, 13000139, 13000294},
		{14, 15, 0, 0}, {15, 16, 0, 0},
	}
	keys, seq, err := a.Diff(b)
	if err != nil || len(seq) != len(want) {
		t.Fatalf("err=%v len=%d want %d", err, len(seq), len(want))
	}
	for i, w := range want {
		if seq[i].Lo != w[0] || seq[i].Hi != w[1] || a.Hash(w[0], w[1]) != w[2] || b.Hash(w[0], w[1]) != w[3] {
			t.Fatalf("row %d [%d,%d) %v hashes %d/%d want %d/%d", i, w[0], w[1], seq[i], a.Hash(w[0], w[1]), b.Hash(w[0], w[1]), w[2], w[3])
		}
	}
	if !reflect.DeepEqual(keys, []int64{1, 2, 4, 13}) {
		t.Fatalf("keys=%v want [1 2 4 13]", keys)
	}
}
func TestIncrementalEqualsRecompute(t *testing.T) { // invariant 2
	for _, c := range [][3]int{{2, 3, 1}, {4, 2, 2}, {3, 4, 3}, {5, 2, 4}} {
		p, _ := mhash.NewParams(c[0], c[1])
		tr, final := fillTree(p, int64(c[2]), 12)
		w := int64(1)
		for range c[1] + 1 {
			for lo := int64(0); lo < p.N; lo += w {
				if g := tr.Hash(lo, lo+w); g != naiveHash(final, c[0], lo, lo+w) {
					t.Fatalf("F=%d D=%d [%d,%d) %d != naive", c[0], c[1], lo, lo+w, g)
				}
			}
			w *= int64(c[0])
		}
	}
}
func TestComparisonSequenceInvariant(t *testing.T) { // invariant 3
	for _, c := range [][3]int{{2, 4, 5}, {4, 3, 6}, {3, 3, 7}} {
		p, _ := mhash.NewParams(c[0], c[1])
		a, _ := fillTree(p, int64(c[2]), 8)
		b, _ := fillTree(p, int64(c[2])+100, 8)
		_, seq, err := a.Diff(b)
		if err != nil {
			t.Fatal(err)
		}
		in := map[Range]bool{}
		for _, x := range seq {
			in[x] = true
		}
		if seq[0] != (Range{Lo: 0, Hi: p.N}) {
			t.Fatal("root not first")
		}
		for _, x := range seq {
			uneq, w := a.Hash(x.Lo, x.Hi) != b.Hash(x.Lo, x.Hi), x.Hi-x.Lo
			if !uneq && w > 1 {
				for i := 0; i < c[0]; i++ {
					ch := Range{Lo: x.Lo + int64(i)*w/int64(c[0]), Hi: x.Lo + int64(i+1)*w/int64(c[0])}
					if in[ch] {
						t.Fatalf("child %v of equal parent %v listed", ch, x)
					}
				}
			}
			if uneq && !(x.Lo == 0 && x.Hi == p.N) {
				pw, lo := w*int64(c[0]), x.Lo-x.Lo%(w*int64(c[0]))
				par := Range{Lo: lo, Hi: lo + pw}
				if !in[par] || a.Hash(par.Lo, par.Hi) == b.Hash(par.Lo, par.Hi) {
					t.Fatalf("range %v lacks unequal parent %v", x, par)
				}
			}
		}
	}
}
func TestDiffCounterBounded(t *testing.T) { // unexported cnt; F=4 D=8
	p, _ := mhash.NewParams(4, 8)
	bound, prev := uint64(4*(1+p.F*p.D)), uint64(0)
	for _, m := range []int{100, 1000, 5000, 10000} {
		a, b, ops := New(p), New(p), make([]Op, m)
		for i := range m {
			ops[i] = Put(int64((i*7919)%int(p.N)), int64(i%3))
		}
		chk(a.ApplyOps(ops, 0))
		chk(b.ApplyOps(ops, 0))
		chk(b.ApplyOps([]Op{Put(12345, 777)}, 0))
		_, _, err := a.Diff(b)
		chk(err)
		if g := a.cnt.Load(); g > bound || (m > 100 && g != prev) {
			t.Fatalf("m=%d cnt=%d bound=%d prev=%d", m, g, bound, prev)
		} else {
			prev = g
		}
	}
}
