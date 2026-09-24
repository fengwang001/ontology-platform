package api_test

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"ontology/api"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func av(t *testing.T, r *api.API, n string, d ...string) { must(t, r.AddView(n, d...)) }

// recompute independently recomputes in declaration order (invariant 1): for
// each dep exactly one of got[d] (a view) and bv[d] (a base) is populated.
func recompute(order []string, dm map[string][]string, bv map[string]int64) map[string]int64 {
	got := map[string]int64{}
	for _, v := range order {
		var s int64
		for _, d := range dm[v] {
			s += got[d] + bv[d]
		}
		got[v] = s
	}
	return got
}
func TestInvariantFullEquivalence(t *testing.T) {
	for _, seed := range []int64{1, 7, 42} {
		rng, r := rand.New(rand.NewSource(seed)), api.New(0)
		deps, order, names := map[string][]string{}, []string{}, []string{"b0", "b1", "b2"}
		for i := 0; i < 24; i++ {
			v := "v" + string(rune('a'+i%26)) + string(rune('a'+i/26))
			var ds []string
			for _, n := range names {
				if rng.Intn(2) == 0 {
					ds = append(ds, n)
				}
			}
			av(t, r, v, ds...)
			deps[v], order, names = ds, append(order, v), append(names, v)
		}
		bv := map[string]int64{}
		for b := 0; b < 12; b++ {
			for _, n := range []string{"b0", "b1", "b2"} {
				if rng.Intn(2) == 0 {
					x := int64(rng.Intn(201) - 100)
					bv[n] = x
					must(t, r.SetBase(n, x))
				}
			}
			r.Refresh()
			if !reflect.DeepEqual(r.View(), recompute(order, deps, bv)) {
				t.Fatalf("seed=%d batch=%d mismatch", seed, b)
			}
		}
	}
}
func TestChangelogTopo(t *testing.T) {
	dcls := [][]string{{"v1", "b1"}, {"v2", "b2"}, {"v3", "v1", "v2"}, {"v4", "v3", "b3"}}
	dm := map[string][]string{"v1": {"b1"}, "v2": {"b2"}, "v3": {"v1", "v2"}, "v4": {"v3", "b3"}}
	cases := []struct {
		chg   []string
		dirty map[string]bool
	}{
		{[]string{"b1"}, map[string]bool{"v1": true, "v3": true, "v4": true}},
		{[]string{"b3"}, map[string]bool{"v4": true}},
		{[]string{"b1", "b3"}, map[string]bool{"v1": true, "v3": true, "v4": true}},
		{[]string{"b1", "b2", "b3"}, map[string]bool{"v1": true, "v2": true, "v3": true, "v4": true}},
	}
	for _, tc := range cases {
		r := api.New(0)
		for _, d := range dcls {
			av(t, r, d[0], d[1:]...)
		}
		for _, b := range tc.chg {
			must(t, r.SetBase(b, 3))
		}
		log, _ := r.Refresh()
		pos := map[string]int{}
		for i, c := range log {
			if _, dup := pos[c.Name]; dup || !tc.dirty[c.Name] {
				t.Fatalf("chg=%v bad entry %v", tc.chg, c)
			}
			pos[c.Name] = i
		}
		if len(pos) != len(tc.dirty) {
			t.Fatalf("chg=%v %d!=%d", tc.chg, len(pos), len(tc.dirty))
		}
		for v := range tc.dirty {
			for _, d := range dm[v] {
				if tc.dirty[d] && pos[d] > pos[v] {
					t.Fatalf("chg=%v %s before dep %s", tc.chg, v, d)
				}
			}
		}
	}
}
func TestRejectedLeavesNoTrace(t *testing.T) {
	full := func() *api.API { r := api.New(2); av(t, r, "v1", "b1"); av(t, r, "v2", "b2"); return r }
	room := func() *api.API { r := api.New(3); av(t, r, "v1", "b1"); return r }
	cases := []struct {
		build func() *api.API
		op    func(*api.API) error
		want  error
	}{
		{full, func(r *api.API) error { return r.AddView("", "b1") }, api.ErrEmptyName},
		{full, func(r *api.API) error { return r.SetBase("ghost", 1) }, api.ErrUnknownName},
		{room, func(r *api.API) error { return r.AddView("v3", "v3") }, api.ErrCycle},
		{full, func(r *api.API) error { return r.AddView("v9", "b1") }, api.ErrTooManyViews},
	}
	seen := map[error]bool{}
	for i, tc := range cases {
		r := tc.build()
		before := r.View()
		if err := tc.op(r); !errors.Is(err, tc.want) || !reflect.DeepEqual(r.View(), before) || seen[tc.want] {
			t.Fatalf("case %d bad", i)
		}
		seen[tc.want] = true
		must(t, r.SetBase("b1", 7))
		r.Refresh()
		if r.View()["v1"] != 7 {
			t.Fatalf("case %d unusable", i)
		}
	}
}
func TestConcurrentReaders(t *testing.T) {
	r := api.New(0)
	for _, d := range [][]string{{"v1", "b1"}, {"v2", "v1", "b2"}} {
		av(t, r, d[0], d[1:]...)
	}
	must(t, r.SetBase("b1", 4))
	must(t, r.SetBase("b2", 5))
	r.Refresh()
	want, res := r.View(), make(chan bool, 64)
	for g := 0; g < 64; g++ {
		go func() { res <- reflect.DeepEqual(r.View(), want) }()
	}
	for g := 0; g < 64; g++ {
		if !<-res {
			t.Fatal("divergent concurrent read")
		}
	}
}
func TestSelfCheck(t *testing.T) { must(t, api.New(0).SelfCheck()) }
