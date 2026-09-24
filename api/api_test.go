package api_test

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/gset"
)

func mk(a, b, c string, m int64) api.Fact { return api.Fact{A: a, B: b, C: c, M: m} }
func ok(t *testing.T, cond bool, msg string, a ...any) {
	t.Helper()
	if !cond {
		t.Fatalf(msg, a...)
	}
}

func refBatch(gs [][]int, seq []api.Fact) map[int]map[string]int64 {
	out := map[int]map[string]int64{}
	for _, dims := range gs {
		g, _ := gset.NewGroup(dims)
		b := map[string]int64{}
		for _, f := range seq {
			b[g.Key([3]string{f.A, f.B, f.C})] += f.M
		}
		for k, s := range b {
			if s == 0 {
				delete(b, k)
			}
		}
		out[g.ID()] = b
	}
	return out
}

func genSeq(r *rand.Rand) []api.Fact {
	w, fs := [...]string{"x", "y", "z"}, make([]api.Fact, 36)
	for i := 0; i < 18; i++ {
		f := mk(w[r.Intn(3)], w[r.Intn(3)], w[r.Intn(3)], int64(1+r.Intn(9)))
		fs[2*i], fs[2*i+1] = f, f
		fs[2*i+1].M = -f.M
	}
	r.Shuffle(18, func(i, j int) { fs[2*i], fs[2*i+1], fs[2*j], fs[2*j+1] = fs[2*j], fs[2*j+1], fs[2*i], fs[2*i+1] })
	return fs
}

func TestBatchEquivalence(t *testing.T) {
	// 不变量 1：随机序列与独立批量重算逐 (组,键) 一致。
	gss := [][][]int{{{0}, {0, 1}, nil}, {{2}, {0, 2}, {0, 1, 2}}, {nil, {1}}}
	for seed := int64(0); seed < 5; seed++ {
		for gi, gs := range gss {
			v, _ := api.New(gs)
			seq := genSeq(rand.New(rand.NewSource(seed*10 + int64(gi))))
			for _, f := range seq {
				ok(t, v.Apply(f) == nil, "apply: %v", f)
			}
			ok(t, reflect.DeepEqual(v.View(), refBatch(gs, seq)), "seed=%d gs=%d view!=batch", seed, gi)
		}
	}
}

func TestGroupCompleteness(t *testing.T) {
	// 不变量 2：组 ID 恰好为指定集合，ID↔GROUPING 三元组一致。
	gs := [][]int{nil, {2}, {0}, {1, 2}}
	v, err := api.New(gs)
	ok(t, err == nil, "New: %v", err)
	want, got := map[int]bool{}, map[int]bool{}
	for _, d := range gs {
		g, _ := gset.NewGroup(d)
		trip := g.Grouping(0)*4 + g.Grouping(1)*2 + g.Grouping(2)
		ok(t, trip == g.ID(), "id=%d triple=%d", g.ID(), trip)
		want[g.ID()] = true
	}
	for id := range v.View() {
		got[id] = true
	}
	ok(t, reflect.DeepEqual(got, want) && !got[0], "ids=%v want=%v", got, want)
}

func TestWithdrawalRejected(t *testing.T) {
	// 不变量 3：撤回越界被拒。
	cases := []struct {
		gs  [][]int
		bad api.Fact
	}{
		{[][]int{{0}, nil}, mk("x", "q", "u", -6)},
		{[][]int{nil}, mk("y", "q", "v", -6)},
		{[][]int{{0, 1}}, mk("x", "p", "z", -6)},
	}
	for i, c := range cases {
		v, _ := api.New(c.gs)
		ok(t, v.Apply(mk("x", "p", "u", 5)) == nil, "seed: %v", i)
		before := v.View()
		ok(t, v.Apply(c.bad) == api.ErrWithdrawn, "case %d not withdrawn", i)
		ok(t, reflect.DeepEqual(v.View(), before), "case %d changed state", i)
	}
}

func TestRejectAtomic(t *testing.T) {
	// 不变量 4：四类错误互异可判、拒绝不留痕、之后仍可用。
	es := []error{api.ErrInvalidGroups, api.ErrEmptyDim, api.ErrZeroMeasure, api.ErrWithdrawn}
	for i := range es {
		for j := i + 1; j < 4; j++ {
			ok(t, es[i] != es[j], "duplicate sentinel %d", i)
		}
	}
	for _, gs := range [][][]int{nil, {{0}, {0}}, {{3}}, {{-1}}, {{0, 0}}} {
		_, err := api.New(gs)
		ok(t, err == api.ErrInvalidGroups, "New(%v)=%v", gs, err)
	}
	v, _ := api.New([][]int{{0}, nil})
	ok(t, v.Apply(mk("x", "p", "u", 5)) == nil, "seed")
	bad := []struct {
		f api.Fact
		w error
	}{
		{mk("", "p", "u", 1), api.ErrEmptyDim}, {mk("x", "", "u", 1), api.ErrEmptyDim},
		{mk("x", "p", "", 1), api.ErrEmptyDim}, {mk("x", "p", "u", 0), api.ErrZeroMeasure},
		{mk("z", "p", "u", -1), api.ErrWithdrawn},
	}
	for i, c := range bad {
		before := v.View()
		ok(t, v.Apply(c.f) == c.w, "bad %d", i)
		ok(t, reflect.DeepEqual(v.View(), before), "bad %d left traces", i)
	}
	ok(t, v.Apply(mk("x", "p", "u", 1)) == nil, "unusable after rejects")
	ok(t, api.SelfCheck() == nil, "SelfCheck failed")
}

func TestConcurrentReaders(t *testing.T) {
	v, _ := api.New([][]int{{0}, {0, 1}, nil})
	for _, f := range genSeq(rand.New(rand.NewSource(99))) {
		ok(t, v.Apply(f) == nil, "seed")
	}
	const n = 32
	start, views := make(chan struct{}), make([]map[int]map[string]int64, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) { defer wg.Done(); <-start; views[i] = v.View() }(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < n; i++ {
		ok(t, reflect.DeepEqual(views[0], views[i]), "reader %d diverged", i)
	}
}
