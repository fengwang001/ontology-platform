package sched_test

import (
	"fmt"
	"math"
	"slices"
	"testing"

	"ontology/sched"
	"ontology/task"
)

func mustAdd(s *sched.Scheduler, id string, w float64) {
	if err := s.AddTenant(id, w); err != nil {
		panic(err)
	}
}

func mustSubmit(s *sched.Scheduler, id string, n int, cost int64) {
	for i := 0; i < n; i++ {
		if err := s.Submit(task.Task{Tenant: id, Cost: cost, Seq: uint64(i)}); err != nil {
			panic(err)
		}
	}
}

func runCounts(s *sched.Scheduler, steps int) map[string]int {
	out := map[string]int{}
	for i := 0; i < steps; i++ {
		t, ok := s.Next()
		if !ok {
			break
		}
		out[t.Tenant]++
	}
	return out
}

func TestFairShare(t *testing.T) {
	cases := []struct {
		name    string
		weights []float64
		steps   int
		extreme bool
	}{
		{"1:3", []float64{1, 3}, 100000, false},
		{"1:3:6", []float64{1, 3, 6}, 100000, false},
		{"1e-9:1e9", []float64{1e-9, 1e9}, 20000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := sched.New()
			var sumW float64
			ids := []string{}
			for i, w := range tc.weights {
				id := fmt.Sprintf("t%d", i)
				ids = append(ids, id)
				mustAdd(s, id, w)
				mustSubmit(s, id, tc.steps, 1)
				sumW += w
			}
			got := runCounts(s, tc.steps)
			if math.IsInf(s.SysVT(), 0) || math.IsNaN(s.SysVT()) {
				t.Fatalf("sysVT not finite: %v", s.SysVT())
			}
			if tc.extreme {
				if got[ids[0]] > 2 {
					t.Errorf("tiny-weight tenant served %d of %d", got[ids[0]], tc.steps)
				}
				return
			}
			for i, w := range tc.weights {
				want := w / sumW
				rel := math.Abs(float64(got[ids[i]])/float64(tc.steps)-want) / want
				if rel >= 0.05 {
					t.Errorf("tenant %s share deviation %.4f >= 0.05", ids[i], rel)
				}
			}
			if len(tc.weights) == 2 {
				ratio := float64(got[ids[1]]) / float64(got[ids[0]])
				want := tc.weights[1] / tc.weights[0]
				if math.Abs(ratio-want)/want >= 0.05 {
					t.Errorf("ratio %.4f not within 5%% of %.0f", ratio, want)
				}
			}
		})
	}
}

func TestRejoinNoMonopoly(t *testing.T) {
	s := sched.New()
	mustAdd(s, "X", 1)
	mustAdd(s, "Y", 1)
	mustSubmit(s, "X", 1, 1)
	mustSubmit(s, "Y", 1200, 1)
	if first, _ := s.Next(); first.Tenant != "X" {
		t.Fatalf("first = %s, want X (tie at vt=0 broken by ID)", first.Tenant)
	}
	runCounts(s, 1000) // X idle while Y executes 1000 tasks
	mustSubmit(s, "X", 50, 1)
	if next, ok := s.Next(); !ok || next.Tenant != "X" {
		t.Fatalf("rejoined X not served within 1 step, got %v", next.Tenant)
	}
	got := runCounts(s, 99)
	if got["Y"] < 40 {
		t.Fatalf("Y starved after X rejoin: %v", got)
	}
	if got["X"] > 60 {
		t.Fatalf("X monopolized after rejoin: %v", got)
	}
}

func TestTieBreakByID(t *testing.T) {
	s := sched.New()
	for _, id := range []string{"b", "a", "c"} {
		mustAdd(s, id, 1)
	}
	for _, id := range []string{"c", "a", "b"} {
		mustSubmit(s, id, 1, 1)
	}
	for i, want := range []string{"a", "b", "c"} {
		if got, ok := s.Next(); !ok || got.Tenant != want {
			t.Fatalf("step %d = %v, want %s", i, got.Tenant, want)
		}
	}
}

func TestSelectionCost(t *testing.T) {
	cases := []struct{ tenants, active int }{
		{1, 1},
		{10, 10},
		{1000, 1000},
		{1000, 5}, // idle tenants must stay out of the heap
	}
	for _, tc := range cases {
		s := sched.New()
		for i := 0; i < tc.tenants; i++ {
			mustAdd(s, fmt.Sprintf("t%04d", i), 1)
		}
		for i := 0; i < tc.active; i++ {
			mustSubmit(s, fmt.Sprintf("t%04d", i), 2, 1)
		}
		if got := s.HeapLen(); got != tc.active {
			t.Fatalf("tenants=%d: heap len = %d, want %d", tc.tenants, got, tc.active)
		}
		s.Next()
		bound := 4 * int(math.Ceil(math.Log2(float64(tc.active))))
		if got := s.LastComparisons(); got > bound {
			t.Errorf("active=%d: comparisons = %d, bound = %d", tc.active, got, bound)
		}
	}
}

func TestZeroCostNoMonopoly(t *testing.T) {
	s := sched.New()
	mustAdd(s, "z", 1)
	mustAdd(s, "n", 1)
	mustSubmit(s, "z", 1000, 0)
	mustSubmit(s, "n", 1000, 1)
	got := runCounts(s, 200)
	if got["n"] < 80 || got["z"] > 120 {
		t.Fatalf("zero-cost tenant monopolized: %v", got)
	}
}

func TestDeterministic(t *testing.T) {
	var ref []string
	for trial := 0; trial < 20; trial++ {
		s := sched.New()
		ids := []string{"a", "b", "c"}
		for i, w := range []float64{1, 2, 3} {
			mustAdd(s, ids[i], w)
		}
		var got []string
		x := uint32(42)
		for i := 0; i < 3000; i++ {
			x = x*1664525 + 1013904223
			if x%3 == 0 {
				if tk, ok := s.Next(); ok {
					got = append(got, tk.Tenant)
				}
			} else {
				mustSubmit(s, ids[int(x%3)], 1, int64(x%5))
			}
		}
		for tk, ok := s.Next(); ok; tk, ok = s.Next() {
			got = append(got, tk.Tenant)
		}
		if trial == 0 {
			ref = got
		} else if !slices.Equal(ref, got) {
			t.Fatalf("trial %d schedule differs", trial)
		}
	}
}
