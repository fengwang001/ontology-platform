package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/hist"
)

func eq(a, b map[string]api.Val) bool { return reflect.DeepEqual(a, b) }
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func mv(t *testing.T, a *api.API, k string, v int) int      { s, e := a.Write(k, v); must(t, e); return s }
func at(t *testing.T, a *api.API, s int) map[string]api.Val { v, e := a.AsOf(s); must(t, e); return v }
func fillAB(t *testing.T, a *api.API)                       { mv(t, a, "a", 1); mv(t, a, "b", 10); mv(t, a, "a", 2) }
func randFill(t *testing.T, a *api.API, seed int64, n, nk int) {
	r, ks := rand.New(rand.NewSource(seed)), []string{"a", "b", "c", "d"}[:nk]
	for i := 1; i <= n; i++ {
		mv(t, a, ks[r.Intn(nk)], i)
	}
}

func TestSelfCheck(t *testing.T) { must(t, api.New().SelfCheck()) }

func TestNaiveReplayRandom(t *testing.T) {
	for _, seed := range []int64{1, 7, 99} {
		r, a := rand.New(rand.NewSource(seed)), api.New()
		type e struct {
			seq int
			key string
			val int
		}
		var log []e
		for i := 1; i <= 300; i++ {
			k := []string{"a", "b", "c", "d"}[r.Intn(4)]
			log = append(log, e{mv(t, a, k, i), k, i})
		}
		replay := func(s int) map[string]api.Val {
			w := map[string]api.Val{}
			for _, x := range log {
				if x.seq <= s {
					w[x.key] = x.val
				}
			}
			return w
		}
		for _, s := range []int{0, 1, a.MaxSeq() / 2, a.MaxSeq(), a.MaxSeq() + 10} {
			if !eq(at(t, a, s), replay(s)) {
				t.Fatalf("seed=%d s=%d mismatch", seed, s)
			}
		}
	}
}

func TestCompactPreserves(t *testing.T) {
	for _, seed := range []int64{3, 14} {
		a := api.New()
		randFill(t, a, seed, 150, 3)
		upto := a.MaxSeq() / 2
		before := map[int]map[string]api.Val{}
		for s := upto + 1; s <= a.MaxSeq()+2; s++ {
			before[s] = at(t, a, s)
		}
		must(t, a.Compact(upto))
		for s, w := range before {
			if !eq(at(t, a, s), w) {
				t.Fatalf("seed=%d compact(%d) s=%d 不一致", seed, upto, s)
			}
		}
	}
}

func TestReachability(t *testing.T) {
	a := api.New()
	fillAB(t, a)
	must(t, a.Compact(2))
	for _, s := range []int{0, 1, 2} {
		if _, err := a.AsOf(s); !errors.Is(err, api.ErrCompacted) {
			t.Fatalf("AsOf(%d) err=%v want ErrCompacted", s, err)
		}
	}
	if !eq(at(t, a, 3), map[string]api.Val{"a": 2, "b": 10}) {
		t.Fatal("AsOf(3) 应仍含基线 b=10")
	}
}

func TestRejectedNoTrace(t *testing.T) {
	a := api.New()
	_, eKey := a.Write("", 1)
	_, eRead := a.AsOf(-1)
	eUpto := a.Compact(-1)
	if !errors.Is(eKey, api.ErrEmptyKey) || !errors.Is(eRead, api.ErrNegativeRead) || !errors.Is(eUpto, api.ErrNegativeCompact) {
		t.Fatalf("sentinels %v %v %v", eKey, eRead, eUpto)
	}
	if a.MaxSeq() != 0 {
		t.Fatalf("被拒后 MaxSeq=%d want 0", a.MaxSeq())
	}
	if seq := mv(t, a, "k", 1); seq != 1 {
		t.Fatalf("恢复写入 seq=%d want 1", seq)
	}
	must(t, a.Compact(0))
	if _, err := a.AsOf(0); !errors.Is(err, api.ErrCompacted) {
		t.Fatalf("AsOf(0) err=%v want ErrCompacted", err)
	}
	if api.ErrEmptyKey == api.ErrNegativeRead || api.ErrEmptyKey == api.ErrCompacted ||
		api.ErrEmptyKey == api.ErrNegativeCompact || api.ErrNegativeRead == api.ErrCompacted ||
		api.ErrNegativeRead == api.ErrNegativeCompact || api.ErrCompacted == api.ErrNegativeCompact {
		t.Fatal("四类哨兵错误必须互不相同")
	}
}

func TestProbeConstant(t *testing.T) { must(t, hist.QuickProbeOK()) }

func TestConcurrentReadsConsistent(t *testing.T) {
	a := api.New()
	randFill(t, a, 5, 400, 3)
	reach := a.MaxSeq() / 2
	must(t, a.Compact(reach-1))
	const N = 64
	var wg sync.WaitGroup
	views := make([]map[string]api.Val, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%5 == 0 {
				a.Write("late", i)
			}
			v, err := a.AsOf(reach)
			if err != nil {
				t.Error(err)
			}
			views[i] = v
		}(g)
	}
	wg.Wait()
	for i := 1; i < N; i++ {
		if !eq(views[i], views[0]) {
			t.Fatalf("goroutine %d 视图不一致", i)
		}
	}
}
