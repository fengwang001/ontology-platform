package api_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
	"ontology/fill"
	"ontology/grid"
)

type scn struct {
	name string
	step int64
	keys []string
	pts  []api.Point
}

var scenarios = []scn{
	{"five-point", 10, []string{"k"}, pts("k", 0, 5, 30, 8, 40, 8, 70, 12, 100, 12)},
	{"negative-ts", 10, []string{"k"}, pts("k", -30, 2, -10, 2, 0, 7)},
	{"single", 10, []string{"k"}, pts("k", 20, 4)},
	{"multi-key", 10, []string{"a", "b"}, append(pts("a", 0, 1, 40, 2), pts("b", 10, 9, 30, 9)...)},
}

func pts(key string, tv ...int64) []api.Point {
	var out []api.Point
	for i := 0; i < len(tv); i += 2 {
		out = append(out, api.Point{Key: key, TS: tv[i], Val: tv[i+1]})
	}
	return out
}

func feed(t *testing.T, s scn) *api.API {
	a, err := api.New(s.step)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Feed(s.pts); err != nil {
		t.Fatal(err)
	}
	return a
}

// naive 逐网格时刻重放 LOCF 的朴素参照。
func naive(key string, step int64, in []api.Point) []api.Point {
	var reals, out []api.Point
	for _, p := range in {
		if p.Key == key {
			reals = append(reals, p)
		}
	}
	ri, cur := 0, reals[0].Val
	for g := reals[0].TS; g <= reals[len(reals)-1].TS; g += step {
		for ri < len(reals) && reals[ri].TS <= g {
			cur, ri = reals[ri].Val, ri+1
		}
		out = append(out, api.Point{Key: key, TS: g, Val: cur})
	}
	return out
}

func eachView(t *testing.T, f func(s scn, k string, v []api.Point)) {
	for _, s := range scenarios {
		a := feed(t, s)
		for _, k := range s.keys {
			f(s, k, a.View(k))
		}
	}
}

func TestViewMatchesNaive(t *testing.T) {
	eachView(t, func(s scn, k string, v []api.Point) {
		if want := naive(k, s.step, s.pts); !reflect.DeepEqual(v, want) {
			t.Fatalf("%s/%s: got %v, want %v", s.name, k, v, want)
		}
	})
}

func TestViewMonotone(t *testing.T) {
	eachView(t, func(s scn, k string, v []api.Point) {
		reals := map[int64]int64{}
		for _, p := range s.pts {
			if p.Key == k {
				reals[p.TS] = p.Val
			}
		}
		prevReal, hasPrev := int64(0), false
		for i, p := range v {
			if i > 0 && (p.TS <= v[i-1].TS || p.Val < v[i-1].Val) {
				t.Fatalf("%s/%s: not monotone at %+v", s.name, k, p)
			}
			if rv, isReal := reals[p.TS]; isReal {
				if rv != p.Val {
					t.Fatalf("%s/%s: real %+v overwritten", s.name, k, p)
				}
				prevReal, hasPrev = p.Val, true
			} else if hasPrev && p.Val != prevReal {
				t.Fatalf("%s/%s: fill %+v != LOCF value %d", s.name, k, p, prevReal)
			}
		}
	})
}

func TestViewGridComplete(t *testing.T) {
	eachView(t, func(s scn, k string, v []api.Point) {
		n := naive(k, s.step, s.pts)
		if len(v) != len(n) || v[0].TS != n[0].TS || v[len(v)-1].TS != n[len(n)-1].TS {
			t.Fatalf("%s/%s: missing/extra grid moment", s.name, k)
		}
		for i := 1; i < len(v); i++ {
			if v[i].TS-v[i-1].TS != s.step {
				t.Fatalf("%s/%s: diff != step at %d", s.name, k, v[i].TS)
			}
		}
	})
}

func TestBadStepDistinct(t *testing.T) {
	if _, err := api.New(0); !errors.Is(err, api.ErrBadStep) {
		t.Fatalf("step=0: %v", err)
	}
	s := []error{api.ErrBadStep, fill.ErrEmptyKey, grid.ErrOffGrid, grid.ErrNotIncreasing}
	if s[0] == s[1] || s[0] == s[2] || s[0] == s[3] || s[1] == s[2] || s[1] == s[3] || s[2] == s[3] {
		t.Fatal("sentinels not distinct")
	}
}

func TestConcurrentView(t *testing.T) {
	a := feed(t, scenarios[3])
	want := map[string][]api.Point{"a": a.View("a"), "b": a.View("b")}
	var wg sync.WaitGroup
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k, w := range want {
				if got := a.View(k); !reflect.DeepEqual(got, w) {
					t.Errorf("%s: concurrent view differs", k)
				}
				a.Last(k)
			}
			_ = a.SelfCheck()
		}()
	}
	wg.Wait()
}
