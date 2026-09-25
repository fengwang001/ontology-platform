package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
)

func fuzz(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	r := api.New()
	ob := make([]api.Obj, 20)
	edge := map[api.Obj]api.Obj{}
	for i := range ob {
		ob[i], _ = r.Root()
	}
	rt := append([]api.Obj{}, ob...)
	for i := 0; i < 250 && len(rt) > 0; i++ {
		switch rng.Intn(3) {
		case 0:
			a, b := rt[rng.Intn(len(rt))], rt[rng.Intn(len(rt))]
			_ = r.Point(a, b)
			edge[a] = b
		case 1:
			j := rng.Intn(len(rt))
			_ = r.Unroot(rt[j])
			rt[j], rt = rt[len(rt)-1], rt[:len(rt)-1]
		default:
			r.Collect()
		}
	}
	r.Collect()
	want, rooted, st := map[api.Obj]bool{}, map[api.Obj]bool{}, []api.Obj{}
	for _, o := range rt {
		want[o], rooted[o], st = true, true, append(st, o)
	}
	for len(st) > 0 {
		x := st[len(st)-1]
		st = st[:len(st)-1]
		if y := edge[x]; y != 0 && !want[y] {
			want[y], st = true, append(st, y)
		}
	}
	for _, o := range ob {
		alive := r.RefCount(o) > 0
		exp := 0
		if rooted[o] {
			exp++
		}
		for x := range want {
			if edge[x] == o {
				exp++
			}
		}
		dangling := alive && edge[o] != 0 && !want[edge[o]]
		if dangling || alive != want[o] || (alive && r.RefCount(o) != exp) {
			t.Fatalf("seed %d: o=%d dangling=%v alive=%v reach=%v rc=%d want=%d", seed, o, dangling, alive, want[o], r.RefCount(o), exp)
		}
	}
}
func fuzzRange(t *testing.T, lo, hi int64) {
	t.Helper()
	for s := lo; s <= hi; s++ {
		fuzz(t, s)
	}
}
func TestInvariantConservation(t *testing.T) { fuzzRange(t, 1, 30) }
func TestInvariantNoDangling(t *testing.T)   { fuzzRange(t, 31, 60) }
func TestInvariantReachability(t *testing.T) { fuzzRange(t, 61, 90) }
func TestEightStepCycle(t *testing.T) {
	r := api.New()
	A, _ := r.Root()
	B, _ := r.Root()
	w := [][2]int{{1, 2}, {2, 2}, {1, 2}, {1, 1}}
	ops := [4]func(){func() { _ = r.Point(A, B) }, func() { _ = r.Point(B, A) }, func() { _ = r.Unroot(A) }, func() { _ = r.Unroot(B) }}
	for i, f := range ops {
		f()
		if r.RefCount(A) != w[i][0] || r.RefCount(B) != w[i][1] {
			t.Fatalf("step %d (%d,%d) want %v", i+2, r.RefCount(A), r.RefCount(B), w[i])
		}
	}
	if r.Collect() != 2 || r.RefCount(A) != 0 || r.RefCount(B) != 0 {
		t.Fatal("ring A<->B not reclaimed")
	}
}
func TestInvariantAtomicity(t *testing.T) {
	r := api.New()
	P, _ := r.Root()
	Q, _ := r.Root()
	_ = r.Point(P, Q)
	_ = r.Unroot(Q) // Q alive via P's edge but rootless
	before := [2]int{r.RefCount(P), r.RefCount(Q)}
	if !errors.Is(r.Unroot(Q), api.ErrNoRootToDrop) {
		t.Fatal("want ErrNoRootToDrop")
	}
	if !errors.Is(r.Point(api.Obj(1<<40), P), api.ErrInvalidObject) {
		t.Fatal("want ErrInvalidObject")
	}
	if [2]int{r.RefCount(P), r.RefCount(Q)} != before {
		t.Fatal("rejected op left a trace")
	}
	l := api.New(1)
	X, _ := l.Root()
	if _, e := l.Root(); !errors.Is(e, api.ErrObjectLimit) || l.RefCount(X) != 1 {
		t.Fatal("limit rejection not atomic")
	}
}
func TestSentinelsDistinct(t *testing.T) {
	if api.ErrInvalidObject == api.ErrNoRootToDrop || api.ErrInvalidObject == api.ErrObjectLimit || api.ErrNoRootToDrop == api.ErrObjectLimit {
		t.Fatal("the three sentinels must be pairwise distinct")
	}
}
func TestConcurrentGraph(t *testing.T) {
	const N = 64
	r := api.New()
	ob := make([]api.Obj, N)
	var wg, rooted sync.WaitGroup
	start := make(chan struct{})
	wg.Add(N)
	rooted.Add(N)
	for i := range ob {
		go func(i int) {
			defer wg.Done()
			ob[i], _ = r.Root()
			rooted.Done()
			<-start
			_ = r.Point(ob[i], ob[(i+1)%N])
		}(i)
	}
	rooted.Wait()
	close(start)
	wg.Wait()
	if r.Collect() != 0 {
		t.Fatal("reachable ring wrongly freed")
	}
	for _, o := range ob {
		if r.RefCount(o) != 2 {
			t.Fatal("rc not conserved under concurrency")
		}
	}
}
func TestSelfCheck(t *testing.T) {
	if e := api.New().SelfCheck(); e != nil {
		t.Fatal(e)
	}
}
