package api_test

import (
	"errors"
	"maps"
	"math/rand"
	"reflect"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

func must(t *testing.T, e error) {
	if e != nil {
		t.Fatal(e)
	}
}
func randReplica(F, D, seed, opsN int) (*api.Replica, map[int64]int64) {
	r, _ := api.New(F, D, 1<<20) // hard-coded valid shape
	n := int64(1)
	for range D {
		n *= int64(F)
	}
	rng, m := rand.New(rand.NewSource(int64(seed))), map[int64]int64{}
	ops := make([]api.Op, opsN)
	for i := range ops { // independent seeds -> presence and value differences
		k, v := rng.Int63n(n), rng.Int63n(41)-20
		ops[i], m[k] = api.Put(k, v), v
	}
	_ = r.Apply(ops) // keys in range, count far below cap
	return r, m
}
func naive(a, b map[int64]int64) []int64 { // per-key presence/value reference
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
func TestDiffNaiveRandom(t *testing.T) { // invariant 1
	for _, c := range [][4]int{{2, 5, 11, 120}, {4, 3, 12, 120}, {3, 4, 13, 200}, {7, 2, 14, 80}} {
		a, am := randReplica(c[0], c[1], c[2], c[3])
		b, bm := randReplica(c[0], c[1], c[2]+99, c[3])
		keys, _, err := a.Diff(b)
		if err != nil || !reflect.DeepEqual(keys, naive(am, bm)) {
			t.Fatalf("F=%d D=%d keys=%v err=%v", c[0], c[1], keys, err)
		}
	}
}
func TestRejectedApplyNoSideEffects(t *testing.T) { // invariant 4
	r, _ := api.New(4, 2, 3) // hard-coded valid shape
	must(t, r.Apply([]api.Op{api.Put(0, 1), api.Put(1, 2), api.Put(2, 3)}))
	before := r.RootHash()
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"F<2", func() error { _, e := api.New(1, 2, 10); return e }, api.ErrInvalidParams},
		{"D<1", func() error { _, e := api.New(4, 0, 10); return e }, api.ErrInvalidParams},
		{"F^D>2^20", func() error { _, e := api.New(2, 21, 10); return e }, api.ErrInvalidParams},
		{"maxKeys<=0", func() error { _, e := api.New(4, 2, 0); return e }, api.ErrInvalidMaxKeys},
		{"key negative", func() error { return r.Apply([]api.Op{api.Put(-1, 1)}) }, api.ErrKeyOutOfRange},
		{"key>=N", func() error { return r.Apply([]api.Op{api.Put(16, 1)}) }, api.ErrKeyOutOfRange},
		{"over maxKeys", func() error { return r.Apply([]api.Op{api.Put(3, 1)}) }, api.ErrMaxKeys},
		{"batch rejected", func() error { return r.Apply([]api.Op{api.Put(0, 9), api.Put(3, 1)}) }, api.ErrMaxKeys},
	}
	for _, c := range cases {
		if err := c.run(); !errors.Is(err, c.want) || r.RootHash() != before {
			t.Fatalf("%s: err=%v root moved", c.name, err)
		}
	}
	if v, ok := r.Get(0); !ok || v != 1 { // rejected overwrite never landed
		t.Fatalf("key 0 = %d,%v", v, ok)
	}
	other, _ := api.New(2, 4, 10)
	if _, _, e := r.Diff(other); !errors.Is(e, api.ErrShapeMismatch) {
		t.Fatalf("shape: %v", e)
	}
	seen := map[error]bool{}
	for _, e := range []error{api.ErrInvalidParams, api.ErrInvalidMaxKeys, api.ErrKeyOutOfRange, api.ErrMaxKeys, api.ErrShapeMismatch} {
		if seen[e] {
			t.Fatal("sentinels not distinct")
		}
		seen[e] = true
	}
	must(t, r.Apply([]api.Op{api.Delete(2)})) // still usable after rejection
	if _, ok := r.Get(2); ok {
		t.Fatal("post-rejection apply failed")
	}
}
func TestSelfCheck(t *testing.T) {
	for _, c := range [][2]int{{4, 2}, {3, 4}, {2, 8}, {5, 2}} {
		r, _ := api.New(c[0], c[1], 1<<20)
		if e := r.SelfCheck(); e != nil {
			t.Fatalf("F=%d D=%d: %v", c[0], c[1], e)
		}
	}
}
func TestConcurrentDiff(t *testing.T) { // no sleeps; Diff races Apply/Get on the same replicas
	a, am := randReplica(4, 3, 31, 100)
	b, bm := randReplica(4, 3, 32, 100)
	k0, s0, err := a.Diff(b)
	must(t, err)
	pick := func(m map[int64]int64) api.Op { // one existing key, overwritten with its own value
		for k, v := range m {
			return api.Put(k, v)
		}
		return api.Put(0, 0)
	}
	oa, ob, N := pick(am), pick(bm), 16
	var wg sync.WaitGroup
	ks, ss := make([][]int64, N), make([][]api.Range, N)
	stop := make(chan struct{})
	tick := func(r *api.Replica, op api.Op) { // logical no-op, real write-lock contention
		for {
			select {
			case <-stop:
				return
			default:
				_ = r.Apply([]api.Op{op})
				_, _ = r.Get(op.K)
			}
		}
	}
	go tick(a, oa)
	go tick(b, ob)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			k, s, e := a.Diff(b)
			if e != nil || !reflect.DeepEqual(k, k0) || !reflect.DeepEqual(s, s0) {
				t.Errorf("goroutine %d inconsistent", g)
			}
			ks[g], ss[g] = k, s
		}(g)
	}
	wg.Wait()
	close(stop)
}
