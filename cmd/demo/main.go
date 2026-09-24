package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/mhash"
)

var fail bool

func report(name string, ok bool) {
	fmt.Printf("%s: %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
	fail = fail || !ok
}
func newRep(F, D, cap int) *api.Replica {
	r, err := api.New(F, D, cap)
	if err != nil {
		panic(err)
	}
	return r
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func rootOf(r *api.Replica, F, D int) int64 { // root hash recomputed from observed Gets
	n := int64(1)
	for range D {
		n *= int64(F)
	}
	cur := make([]int64, n)
	for k := range n {
		if v, ok := r.Get(k); ok {
			cur[k] = mhash.LeafHash(k, v)
		}
	}
	for len(cur) > 1 {
		nxt := make([]int64, len(cur)/F)
		for j := range nxt {
			nxt[j] = mhash.CombineNode(cur[j*F : (j+1)*F])
		}
		cur = nxt
	}
	return cur[0]
}
func main() {
	a, b := newRep(4, 2, 1000), newRep(4, 2, 1000)
	must(a.Apply([]api.Op{api.Put(1, 5), api.Put(2, 5), api.Put(4, 0), api.Put(9, 7), api.Put(13, 3)}))
	must(b.Apply([]api.Op{api.Put(1, 6), api.Put(2, 4), api.Put(9, 7), api.Put(13, 8)}))
	keys, seq, err := a.Diff(b)
	report("section3 roots 322749291/477521251, 17 ranges, keys {1,2,4,13}",
		err == nil && a.RootHash() == 322749291 && b.RootHash() == 477521251 &&
			len(seq) == 17 && reflect.DeepEqual(keys, []int64{1, 2, 4, 13}))

	z, empty := newRep(4, 1, 100), newRep(4, 1, 100)
	must(z.Apply([]api.Op{api.Put(2, 0)})) // present key with v=0 vs missing key
	kd, _, _ := z.Diff(empty)
	report("zero-valued key distinguishable from missing", rootOf(z, 4, 1) != 0 && len(kd) == 1 && kd[0] == 2)

	rng := rand.New(rand.NewSource(7))
	x, y := newRep(3, 4, 100000), newRep(3, 4, 100000)
	for _, r := range []*api.Replica{x, y} {
		ops := make([]api.Op, 200)
		for j := range ops {
			ops[j] = api.Put(rng.Int63n(81), rng.Int63n(9)-4)
		}
		must(r.Apply(ops))
	}
	kx, _, _ := x.Diff(y)
	var want []int64 // naive per-key comparison
	for k := range int64(81) {
		va, oa := x.Get(k)
		vb, ob := y.Get(k)
		if oa != ob || va != vb {
			want = append(want, k)
		}
	}
	report("random data matches naive per-key comparison", reflect.DeepEqual(kx, want))

	incOK := true
	for _, op := range []api.Op{api.Put(7, 9), api.Delete(1), api.Put(2, 100), api.Delete(50)} {
		must(x.Apply([]api.Op{op}))
		incOK = incOK && x.RootHash() == rootOf(x, 3, 4)
	}
	report("incremental hash equals from-scratch recompute", incOK)

	_, e1 := api.New(1, 2, 10)
	_, e2 := api.New(2, 1, 0)
	e3 := y.Apply([]api.Op{api.Put(81, 1)})
	full := newRep(2, 1, 1)
	must(full.Apply([]api.Op{api.Put(0, 1)}))
	e4 := full.Apply([]api.Op{api.Put(1, 1)})
	_, _, e5 := x.Diff(newRep(4, 2, 100))
	report("four decidable errors (params/maxKeys/key/shape)",
		e1 == api.ErrInvalidParams && e2 == api.ErrInvalidMaxKeys && e3 == api.ErrKeyOutOfRange &&
			e4 == api.ErrMaxKeys && e5 == api.ErrShapeMismatch)

	before := y.RootHash()
	traceOK := y.Apply([]api.Op{api.Put(-1, 1), api.Put(0, 9)}) == api.ErrKeyOutOfRange &&
		y.RootHash() == before && y.RootHash() == rootOf(y, 3, 4)
	report("rejected operation leaves no state trace", traceOK)

	boundOK := true
	for _, m := range []int{100, 1000, 5000, 10000} { // F=4,D=8; unexported cnt == 2*len(seq)
		p, q := newRep(4, 8, m+10), newRep(4, 8, m+10)
		ops := make([]api.Op, m)
		for i := range m {
			ops[i] = api.Put(int64((i*7919)%(1<<16)), int64(i%3))
		}
		must(p.Apply(ops))
		must(q.Apply(ops))
		must(q.Apply([]api.Op{api.Put(12345, 777)}))
		_, sq, _ := p.Diff(q)
		boundOK = boundOK && len(sq) <= 2*(1+4*8) // cnt <= 4*(1+F*D)
	}
	report("counter bounded independent of m (m=100..10000)", boundOK)

	res := make([][]int64, 8)
	sqs := make([][]api.Range, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) { defer wg.Done(); res[g], sqs[g], _ = a.Diff(b) }(g)
	}
	wg.Wait()
	concOK := true
	for g := 1; g < 8; g++ {
		concOK = concOK && reflect.DeepEqual(res[g], res[0]) && reflect.DeepEqual(sqs[g], sqs[0])
	}
	report("concurrent Diffs are field-identical", concOK)

	if fail {
		os.Exit(1)
	}
}
