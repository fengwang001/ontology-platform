package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/dag"
	"ontology/view"
)

func build(t *testing.T) *Engine {
	e := New()
	add := func(n string, d []string, f func(...int64) int64) {
		if err := e.AddView(n, d, f); err != nil {
			t.Fatal(err)
		}
	}
	add("A", nil, nil)
	add("B", nil, nil)
	add("E", []string{"C", "D"}, func(a ...int64) int64 { return a[0] + a[1] })
	add("F", []string{"D"}, func(a ...int64) int64 { return a[0] - 1 })
	add("C", []string{"A", "B"}, func(a ...int64) int64 { return a[0] + a[1] })
	add("D", []string{"A"}, func(a ...int64) int64 { return a[0] * 2 })
	return e
}

func get(t *testing.T, e *Engine, n string) int64 {
	v, _, err := e.Get(n)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// TestErrorsDistinct：四类故障注入可判定且互不相同。
func TestErrorsDistinct(t *testing.T) {
	e := build(t)
	if err := e.AddView("M", []string{"N"}, nil); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ err, want error }{
		{e.AddView("", nil, nil), ErrEmptyName},
		{e.AddView("A", nil, nil), ErrDuplicateName},
		{e.AddView("N", []string{"M"}, nil), dag.ErrCycle},
		{e.Set("zz", 1), ErrUnknownView},
		{func() error { _, _, err := e.Get("zz"); return err }(), ErrUnknownView},
	}
	for i, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("case %d: %v want %v", i, c.err, c.want)
		}
	}
	sents := []error{ErrEmptyName, ErrDuplicateName, dag.ErrCycle, ErrUnknownView, view.ErrUnresolved}
	for i, si := range sents {
		for j, sj := range sents {
			if i != j && errors.Is(si, sj) {
				t.Fatal("sentinels not distinct")
			}
		}
	}
}

// TestFailureLeavesNoTrace：被拒操作不改变任何状态，引擎仍可正常使用。
func TestFailureLeavesNoTrace(t *testing.T) {
	e := build(t)
	if err := errors.Join(e.Set("A", 1), e.Set("B", 2), e.Recompute()); err != nil {
		t.Fatal(err)
	}
	before := map[string]int64{}
	for _, n := range []string{"A", "B", "C", "D", "E", "F"} {
		before[n] = get(t, e, n)
	}
	_ = e.AddView("", nil, nil)
	_ = e.AddView("A", nil, nil)
	_ = e.AddView("G", []string{"G"}, nil)
	_ = e.Set("zz", 9)
	_ = e.Set("C", 9)
	for n, w := range before {
		if get(t, e, n) != w {
			t.Fatalf("%s changed to %d want %d", n, get(t, e, n), w)
		}
	}
	if err := errors.Join(e.Set("B", 5), e.Recompute()); err != nil {
		t.Fatal("engine unusable after rejections")
	}
	if get(t, e, "C") != 6 {
		t.Fatalf("C=%d want 6", get(t, e, "C"))
	}
}

// TestBatchConsistency：随机操作序列对拍朴素全量拓扑重算（不变量 1）。
func TestBatchConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 20; trial++ {
		e := build(t)
		base := map[string]int64{}
		for i := 0; i < 30; i++ {
			n := string(rune('A' + rng.Intn(2)))
			base[n] = int64(rng.Intn(50))
			if err := errors.Join(e.Set(n, base[n]), e.Recompute()); err != nil {
				t.Fatal(err)
			}
		}
		a, b := base["A"], base["B"]
		want := map[string]int64{"A": a, "B": b, "C": a + b, "D": 2 * a, "E": 3*a + b, "F": 2*a - 1}
		for n, w := range want {
			if got := get(t, e, n); got != w {
				t.Fatalf("trial %d: %s=%d want %d", trial, n, got, w)
			}
		}
	}
}

// TestConcurrentGet：写者反复 Set+Recompute，读者不得读到半轮中间态。
func TestConcurrentGet(t *testing.T) {
	e := New()
	_ = e.AddView("A", nil, nil)
	_ = e.AddView("D", []string{"A"}, func(a ...int64) int64 { return a[0] * 2 })
	const k = 300
	start, bad := make(chan struct{}), make(chan int64, 64)
	var wg sync.WaitGroup
	work := func(write bool) {
		defer wg.Done()
		<-start
		for i := 1; i <= k; i++ {
			if write {
				_ = errors.Join(e.Set("A", int64(i)), e.Recompute())
			} else if v, _, err := e.Get("D"); err != nil || v%2 != 0 || v > 2*k {
				bad <- v // 合法值集合 = {0,2,4,...,2k}
			}
			_ = e.SelfCheck()
		}
	}
	wg.Add(9)
	go work(true)
	for r := 0; r < 8; r++ {
		go work(false)
	}
	close(start)
	wg.Wait()
	close(bad)
	for v := range bad {
		t.Fatalf("illegal read %d", v)
	}
	if got := get(t, e, "D"); got != 2*k {
		t.Fatalf("final D=%d want %d", got, 2*k)
	}
}
