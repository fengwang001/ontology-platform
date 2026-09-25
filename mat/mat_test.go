package mat

import (
	"fmt"
	"sync"
	"testing"

	"ontology/dep"
)

func mustGraph(t *testing.T, spec dep.Spec) *dep.Graph {
	t.Helper()
	g, err := dep.Build(spec)
	if err != nil {
		t.Fatalf("dep.Build: %v", err)
	}
	return g
}

func chainSpec(m int) dep.Spec {
	inc := func(v []int) int { return v[0] + 1 }
	cs := []dep.Column{{Name: "a", Base: true, Initial: 0}}
	pre := "a"
	for i := 1; i <= m; i++ {
		n := fmt.Sprintf("c%d", i)
		cs = append(cs, dep.Column{Name: n, Deps: []string{pre}, Fn: inc})
		pre = n
	}
	return dep.Spec{Columns: cs}
}

// TestChainCounter 钉住复杂度：失效后只按需重算被请求的列，与链长 m 无关。
func TestChainCounter(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e := New(mustGraph(t, chainSpec(m)))
		cm := fmt.Sprintf("c%d", m)
		v, err := e.Get(cm) // 整条链算全
		if err != nil || v != m || e.lastGetCalls != m {
			t.Fatalf("m=%d Get(%s)=%d,%v calls=%d want %d", m, cm, v, err, e.lastGetCalls, m)
		}
		if v, _ := e.Get("c1"); v != 1 || e.lastGetCalls != 0 {
			t.Fatalf("m=%d cached calls=%d want 0", m, e.lastGetCalls) // 全缓存 0 次
		}
		if err := e.Set("a", 0); err != nil { // 整条链失效
			t.Fatal(err)
		}
		if v, _ := e.Get("c1"); v != 1 || e.lastGetCalls != 1 { // 只重算 c1
			t.Fatalf("m=%d post-invalidate calls=%d want 1", m, e.lastGetCalls)
		}
	}
}

func sevenSpec() dep.Spec {
	sum := func(v []int) int { return v[0] + v[1] }
	return dep.Spec{Columns: []dep.Column{
		{Name: "a", Base: true, Initial: 2}, {Name: "b", Base: true, Initial: 3},
		{Name: "c", Base: true, Initial: 4},
		{Name: "d", Deps: []string{"a", "b"}, Fn: sum},
		{Name: "e", Deps: []string{"d"}, Fn: func(v []int) int { return v[0] * 2 }},
		{Name: "f", Deps: []string{"c", "e"}, Fn: sum},
	}}
}

func TestCacheConsistency(t *testing.T) {
	e := New(mustGraph(t, sevenSpec()))
	cases := []struct {
		get string
		v   int
		c   int
	}{
		{"d", 5, 1}, {"d", 5, 0}, {"e", 10, 1}, {"f", 14, 1},
	}
	for i, c := range cases {
		if v, err := e.Get(c.get); err != nil || v != c.v || e.lastGetCalls != c.c {
			t.Fatalf("case %d Get(%s)=%d,%v calls=%d want %d,%d", i, c.get, v, err, e.lastGetCalls, c.v, c.c)
		}
	}
	if err := e.Set("c", 40); err != nil { // 仅 f 依赖 c
		t.Fatal(err)
	}
	if v, _ := e.Get("e"); v != 10 || e.lastGetCalls != 0 { // e 缓存保留
		t.Fatalf("unrelated e invalidated: v=%d calls=%d", v, e.lastGetCalls)
	}
	if v, _ := e.Get("f"); v != 50 || e.lastGetCalls != 1 { // 仅重算 f
		t.Fatalf("f=%d calls=%d want 50,1", v, e.lastGetCalls)
	}
}

// TestInvalidation：Set 基列后直接与间接依赖都取新值。
func TestInvalidation(t *testing.T) {
	e := New(mustGraph(t, sevenSpec()))
	for _, n := range []string{"f", "d", "e"} {
		if _, err := e.Get(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Set("a", 5); err != nil { // 传递失效 d,e,f
		t.Fatal(err)
	}
	for n, w := range map[string]int{"d": 8, "e": 16, "f": 20} {
		if v, _ := e.Get(n); v != w {
			t.Fatalf("after Set(a): Get(%s)=%d want %d", n, v, w)
		}
	}
}

func TestRejectedAtomic(t *testing.T) {
	e := New(mustGraph(t, sevenSpec()))
	e.Get("f")
	snap := map[string]int{"a": 2, "b": 3, "c": 4, "d": 5, "e": 10, "f": 14}
	if _, err := e.Get("nope"); err != ErrUnknownColumn {
		t.Fatalf("Get unknown err=%v", err)
	}
	if err := e.Set("nope", 1); err != ErrUnknownColumn {
		t.Fatalf("Set unknown err=%v", err)
	}
	if err := e.Set("d", 9); err != ErrSetDerived {
		t.Fatalf("Set derived err=%v", err)
	}
	for n, w := range snap {
		if v, _ := e.Get(n); v != w || e.lastGetCalls != 0 {
			t.Fatalf("state changed at %s: %d calls=%d want %d", n, v, e.lastGetCalls, w)
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	e := New(mustGraph(t, sevenSpec()))
	names := []string{"a", "b", "c", "d", "e", "f"}
	for _, n := range names {
		e.Get(n)
	}
	ref := map[string]int{"a": 2, "b": 3, "c": 4, "d": 5, "e": 10, "f": 14}
	const N = 16
	var wg sync.WaitGroup
	wg.Add(N)
	for g := 0; g < N; g++ {
		go func() {
			defer wg.Done()
			for r := 0; r < 200; r++ {
				for _, n := range names {
					if v, err := e.Get(n); err != nil || v != ref[n] {
						t.Errorf("%s=%d,%v want %d", n, v, err, ref[n])
					}
				}
			}
		}()
	}
	wg.Wait()
}
