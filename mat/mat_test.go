package mat

import (
	"fmt"
	"testing"

	"ontology/dep"
)

func spec() dep.Spec {
	return dep.Spec{
		Base: map[string]int{"a": 2, "b": 3, "c": 4},
		Derived: map[string]dep.Derived{
			"d": {Deps: []string{"a", "b"}, Fn: func(v []int) int { return v[0] + v[1] }},
			"e": {Deps: []string{"d"}, Fn: func(v []int) int { return v[0] * 2 }},
			"f": {Deps: []string{"c", "e"}, Fn: func(v []int) int { return v[0] + v[1] }},
		},
	}
}

var cols = []string{"a", "b", "c", "d", "e", "f"}

// naive 朴素全量即时重算，作为对照基准。
func naive(s dep.Spec, base map[string]int, n string) int {
	if v, ok := base[n]; ok {
		return v
	}
	d := s.Derived[n]
	args := make([]int, len(d.Deps))
	for i, dn := range d.Deps {
		args[i] = naive(s, base, dn)
	}
	return d.Fn(args)
}
func checkAll(t *testing.T, e *Engine, s dep.Spec, base map[string]int) {
	t.Helper()
	for _, c := range cols {
		got, err := e.Get(c)
		if want := naive(s, base, c); err != nil || got != want {
			t.Fatalf("%s=%d (naive=%d) err=%v", c, got, want, err)
		}
	}
}
func TestNaiveConsistency(t *testing.T) {
	s := spec()
	e, _ := NewEngine(s)
	base := map[string]int{"a": 2, "b": 3, "c": 4}
	ops := []struct {
		set bool
		col string
		v   int
	}{
		{false, "e", 0}, {false, "d", 0}, {false, "f", 0},
		{true, "a", 5}, {false, "d", 0}, {false, "f", 0}, {false, "e", 0},
		{true, "c", 10}, {false, "f", 0}, {true, "b", 100}, {false, "d", 0},
	}
	for _, op := range ops {
		if op.set {
			if err := e.Set(op.col, op.v); err != nil {
				t.Fatal(err)
			}
			base[op.col] = op.v
		} else if _, err := e.Get(op.col); err != nil {
			t.Fatal(err)
		}
		checkAll(t, e, s, base)
	}
}
func TestCacheConsistency(t *testing.T) {
	s := spec()
	e, _ := NewEngine(s)
	base := map[string]int{"a": 2, "b": 3, "c": 4}
	checkAll(t, e, s, base) // 缓存就绪
	for i := 0; i < 3; i++ {
		checkAll(t, e, s, base) // 缓存有效期间不得背离朴素结果
		if e.lastFn != 0 {
			t.Fatalf("cached Get recomputed %d times", e.lastFn)
		}
	}
}
func TestInvalidation(t *testing.T) {
	s := spec()
	e, _ := NewEngine(s)
	base := map[string]int{"a": 2, "b": 3, "c": 4}
	checkAll(t, e, s, base)
	if err := e.Set("c", 40); err != nil { // 只失效 f；d、e 缓存必须保持
		t.Fatal(err)
	}
	base["c"] = 40
	for _, c := range []string{"d", "e"} {
		if _, err := e.Get(c); err != nil || e.lastFn != 0 {
			t.Fatalf("%s recomputed after Set(c)", c)
		}
	}
	checkAll(t, e, s, base)               // f 必须取新值
	if err := e.Set("a", 9); err != nil { // 失效 d,e,f
		t.Fatal(err)
	}
	base["a"] = 9
	checkAll(t, e, s, base)
}
func TestFailureAtomic(t *testing.T) {
	e, _ := NewEngine(spec())
	before := map[string]int{}
	for _, c := range cols {
		before[c], _ = e.Get(c)
	}
	rejected := []struct{ got, want error }{
		{func() error { _, err := e.Get("zzz"); return err }(), ErrUnknownColumn},
		{e.Set("zzz", 1), ErrUnknownColumn},
		{e.Set("d", 1), ErrSetDerived},
	}
	for i, r := range rejected {
		if r.got != r.want {
			t.Fatalf("op %d: got %v want %v", i, r.got, r.want)
		}
	}
	for _, c := range cols {
		if v, _ := e.Get(c); v != before[c] {
			t.Fatalf("%s changed after rejected ops", c)
		}
	}
}
func chainSpec(m int) dep.Spec {
	s := dep.Spec{Base: map[string]int{"a": 1}, Derived: map[string]dep.Derived{}}
	prev := "a"
	for i := 1; i <= m; i++ {
		n := fmt.Sprintf("c%d", i)
		s.Derived[n] = dep.Derived{Deps: []string{prev}, Fn: func(v []int) int { return v[0] + 1 }}
		prev = n
	}
	return s
}
func TestChainRecompute(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		e, _ := NewEngine(chainSpec(m))
		if _, err := e.Get(fmt.Sprintf("c%d", m)); err != nil || e.lastFn != m {
			t.Fatalf("m=%d: full chain lastFn=%d err=%v", m, e.lastFn, err)
		}
		if _, _ = e.Get("c1"); e.lastFn != 0 {
			t.Fatalf("m=%d: cached Get(c1) lastFn=%d", m, e.lastFn)
		}
		if err := e.Set("a", 7); err != nil {
			t.Fatal(err)
		}
		if v, _ := e.Get("c1"); v != 8 || e.lastFn != 1 {
			t.Fatalf("m=%d: after invalidate Get(c1)=%d lastFn=%d", m, v, e.lastFn)
		}
	}
}
