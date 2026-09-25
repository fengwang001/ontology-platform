package schedule

import (
	"fmt"
	"slices"
	"testing"

	"ontology/hook"
	"ontology/snapshot"
)

func mk(t *testing.T, r *hook.Registry, name, typ string, phase hook.Phase, prio int) {
	t.Helper()
	h, err := hook.New(name, typ, phase, prio, func(*snapshot.Snapshot) (bool, string) {
		return true, ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register(h); err != nil {
		t.Fatal(err)
	}
}

func names(hooks []hook.Hook) []string {
	out := make([]string, len(hooks))
	for i, h := range hooks {
		out[i] = h.Name()
	}
	return out
}

func TestPlanOrdering(t *testing.T) {
	cases := []struct {
		name string
		defs []struct {
		name  string
		phase hook.Phase
		prio  int
	}
	want []string
	}{
		{
			"pre before post",
			[]struct {
				name  string
				phase hook.Phase
				prio  int
			}{
				{"post-low", hook.PhasePost, 0},
				{"pre-low", hook.PhasePre, 0},
				{"post-high", hook.PhasePost, 9},
				{"pre-high", hook.PhasePre, 9},
			},
			[]string{"pre-high", "pre-low", "post-high", "post-low"},
		},
		{
			"priority desc then registration asc",
			[]struct {
				name  string
				phase hook.Phase
				prio  int
			}{
				{"a", hook.PhasePre, 1},
				{"b", hook.PhasePre, 5},
				{"c", hook.PhasePre, 5},
				{"d", hook.PhasePre, 1},
			},
			[]string{"b", "c", "a", "d"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := hook.NewRegistry()
			for _, d := range tc.defs {
				mk(t, r, d.name, "task", d.phase, d.prio)
			}
			got := names(Plan(r.Matching("task")))
			if !slices.Equal(got, tc.want) {
				t.Fatalf("order = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPlanOrderIndependentOfRegistrationOrder(t *testing.T) {
	specs := []struct {
		name  string
		phase hook.Phase
		prio  int
	}{
		{"p1", hook.PhasePre, 0},
		{"p2", hook.PhasePre, 0},
		{"p3", hook.PhasePre, 5},
		{"q1", hook.PhasePost, 0},
		{"q2", hook.PhasePost, 9},
	}
	orders := [][]int{{0, 1, 2, 3, 4}, {4, 3, 2, 1, 0}, {2, 0, 4, 1, 3}}
	var want []Entry
	for oi, order := range orders {
		r := hook.NewRegistry()
		for _, idx := range order {
			s := specs[idx]
			mk(t, r, s.name, "task", s.phase, s.prio)
		}
		got := Signature(Plan(r.Matching("task")))
		if oi == 0 {
			want = got
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("registration order %d: %v, want %v", oi, got, want)
		}
	}

	// Plan must not mutate its input.
	r := hook.NewRegistry()
	for _, s := range specs {
		mk(t, r, s.name, "task", s.phase, s.prio)
	}
	in := r.Matching("task")
	before := names(in)
	_ = Plan(in)
	if !slices.Equal(names(in), before) {
		t.Fatal("Plan reordered the input slice in place")
	}
}
