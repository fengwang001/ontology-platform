package view

import (
	"errors"
	"fmt"
	"testing"

	"ontology/dag"
)

func sumv(x ...int64) (t int64) {
	for _, v := range x {
		t += v
	}
	return
}
func dblf(x ...int64) int64 { return x[0] * 2 }
func decf(x ...int64) int64 { return x[0] - 1 }

type spec struct {
	n string
	d []string
	f func(...int64) int64
}

var phase3 = []spec{{"A", nil, nil}, {"B", nil, nil}, {"E", []string{"C", "D"}, sumv},
	{"F", []string{"D"}, decf}, {"C", []string{"A", "B"}, sumv}, {"D", []string{"A"}, dblf}}

func addViews(t *testing.T, s *Store, vs []spec) {
	t.Helper()
	for _, v := range vs {
		if err := s.AddView(v.n, v.d, v.f); err != nil {
			t.Fatal(err)
		}
	}
}

func TestThreePhases(t *testing.T) {
	s := New()
	addViews(t, s, phase3)
	want := [][6]int64{{1, 2, 3, 2, 5, 1}, {1, 20, 21, 2, 23, 1}, {10, 20, 30, 20, 50, 19}}
	for i, ab := range [][2]int64{{1, 2}, {1, 20}, {10, 20}} {
		if err := s.Set("A", ab[0]); err != nil || s.Set("B", ab[1]) != nil || s.Recompute() != nil {
			t.Fatal("phase", i)
		}
		for j, n := range []string{"A", "B", "C", "D", "E", "F"} {
			g, ok, err := s.Get(n)
			if err != nil || !ok || g != want[i][j] {
				t.Errorf("phase %d %s=%d want %d", i, n, g, want[i][j])
			}
		}
	}
}

// TestDedup pins invariant 3: each dirty fn at most once/round; clean ones never.
func TestDedup(t *testing.T) {
	cases := []struct {
		v    []spec
		base string
		want int
	}{
		{[]spec{{"a", nil, nil}, {"b", []string{"a"}, dblf}, {"c", []string{"a"}, dblf},
			{"v", []string{"b", "c"}, sumv}}, "a", 3}, // diamond: v invalidated twice
		{[]spec{{"X", nil, nil}, {"Y", []string{"X"}, dblf}, {"Z", []string{"Y"}, dblf}}, "X", 2},
	}
	for ti, tc := range cases {
		s, calls := New(), map[string]int{}
		for _, v := range tc.v {
			f := v.f
			if f != nil {
				n, g := v.n, v.f
				f = func(x ...int64) int64 { calls[n]++; return g(x...) }
			}
			if err := s.AddView(v.n, v.d, f); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Set(tc.base, 1); err != nil || s.Recompute() != nil {
			t.Fatal(err)
		}
		if s.evalCount != tc.want {
			t.Errorf("case %d: evalCount=%d want %d", ti, s.evalCount, tc.want)
		}
		for n, c := range calls {
			if c != 1 {
				t.Errorf("case %d: %s evaluated %d times", ti, n, c)
			}
		}
		if s.Recompute() != nil || s.evalCount != 0 {
			t.Errorf("clean round evaluated %d views", s.evalCount)
		}
	}
}

// TestEvalCountScales: evalCount for Set(X) is 2 (Y,Z) regardless of m.
func TestEvalCountScales(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			if err := s.AddView(fmt.Sprintf("W%d", i), nil, nil); err != nil {
				t.Fatal(err)
			}
		}
		addViews(t, s, []spec{{"X", nil, nil}, {"Y", []string{"X"}, dblf}, {"Z", []string{"Y"}, dblf}})
		if err := s.Set("X", 1); err != nil || s.Recompute() != nil || s.evalCount != 2 {
			t.Errorf("m=%d: evalCount=%d want 2 (must not grow with m)", m, s.evalCount)
		}
	}
}

// TestRejectedOpsNoTrace pins invariant 4: rejected ops change nothing.
func TestRejectedOpsNoTrace(t *testing.T) {
	s := New()
	addViews(t, s, phase3)
	if err := s.Set("A", 1); err != nil || s.Set("B", 2) != nil || s.Recompute() != nil {
		t.Fatal(err)
	}
	if err := s.AddView("M", []string{"N"}, nil); err != nil { // forward ref allowed
		t.Fatal(err)
	}
	cases := []struct {
		op   func() error
		want error
	}{
		{func() error { return s.AddView("", nil, nil) }, dag.ErrEmptyName},
		{func() error { return s.AddView("A", nil, nil) }, dag.ErrExists},
		{func() error { return s.AddView("N", []string{"M"}, nil) }, dag.ErrCycle},
		{func() error { return s.Set("z", 1) }, ErrUnresolved},
		{func() error { _, _, e := s.Get("z"); return e }, ErrUnresolved},
	}
	for i, tc := range cases {
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Errorf("case %d: got %v want %v", i, err, tc.want)
		}
	}
	if s.g.Has("N") {
		t.Error("rejected N left a trace")
	}
	if e, _, _ := s.Get("E"); e != 5 || s.Recompute() != nil {
		t.Error("state changed or unusable after rejection")
	}
	u := New() // a failed Recompute (unresolved dep) also leaves no trace
	addViews(t, u, []spec{{"p", nil, nil}, {"q", []string{"p", "ghost"}, sumv}})
	if err := u.Set("p", 1); err != nil || !errors.Is(u.Recompute(), ErrUnresolved) {
		t.Fatal("want ErrUnresolved")
	}
	if u.evalCount != 0 || !u.dirty["q"] || u.hasVal["q"] {
		t.Errorf("failed recompute left trace: %d/%v/%v", u.evalCount, u.dirty["q"], u.hasVal["q"])
	}
}
