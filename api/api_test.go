package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/dep"
	"ontology/mat"
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

func TestSelfCheck(t *testing.T) {
	e, err := api.New(spec())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestErrorsDistinct(t *testing.T) {
	e, _ := api.New(spec())
	fn0 := func(v []int) int { return v[0] }
	cyc := dep.Spec{Derived: map[string]dep.Derived{
		"x": {Deps: []string{"y"}, Fn: fn0},
		"y": {Deps: []string{"x"}, Fn: fn0}}}
	bad := dep.Spec{Base: map[string]int{"a": 1}, Derived: map[string]dep.Derived{
		"x": {Deps: []string{"y"}, Fn: fn0}}}
	_, gerr := e.Get("zzz")
	_, cerr := api.New(cyc)
	_, uerr := api.New(bad)
	cases := []struct {
		name string
		got  error
		want error
	}{
		{"get unknown column", gerr, mat.ErrUnknownColumn},
		{"set unknown column", e.Set("zzz", 1), mat.ErrUnknownColumn},
		{"set derived column", e.Set("d", 1), mat.ErrSetDerived},
		{"cyclic dependency", cerr, dep.ErrCycle},
		{"undeclared dependency", uerr, dep.ErrUnknownDep},
	}
	for _, c := range cases {
		if !errors.Is(c.got, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
	sentinels := []error{mat.ErrUnknownColumn, mat.ErrSetDerived, dep.ErrCycle, dep.ErrUnknownDep}
	for i, a := range sentinels {
		for _, b := range sentinels[i+1:] {
			if a == b {
				t.Fatalf("sentinels %v and %v not distinct", a, b)
			}
		}
	}
}

func TestConcurrentGet(t *testing.T) {
	e, _ := api.New(spec())
	want := map[string]int{}
	for _, c := range []string{"a", "b", "c", "d", "e", "f"} {
		want[c], _ = e.Get(c) // 缓存就绪
	}
	var wg sync.WaitGroup
	bad := make(chan string, 256)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 50; r++ {
				for c, w := range want {
					if v, err := e.Get(c); err != nil || v != w {
						bad <- c
					}
				}
			}
			if err := e.SelfCheck(); err != nil {
				bad <- "selfcheck"
			}
		}()
	}
	wg.Wait()
	select {
	case c := <-bad:
		t.Fatalf("concurrent read mismatch on %s", c)
	default:
	}
}
