package sched

import (
	"container/heap"
	"fmt"
	"math"
	"testing"

	"ontology/task"
	"ontology/tenant"
)

func drain(s *Scheduler, n int) []task.Task {
	out := make([]task.Task, 0, n)
	for i := 0; i < n; i++ {
		tsk, ok := s.Next()
		if !ok {
			break
		}
		out = append(out, tsk)
	}
	return out
}

func maxRun(out []task.Task, id string) int {
	best, cur := 0, 0
	for _, tsk := range out {
		if tsk.Tenant == id {
			cur++
			if cur > best {
				best = cur
			}
		} else {
			cur = 0
		}
	}
	return best
}

func counts(out []task.Task) map[string]int {
	m := map[string]int{}
	for _, tsk := range out {
		m[tsk.Tenant]++
	}
	return m
}

func TestConvergence(t *testing.T) {
	cases := []struct {
		name    string
		weights map[string]float64
		steps   int
		want    map[string]int // exact counts when non-nil
		tol     float64
	}{
		{"1:3", map[string]float64{"A": 1, "B": 3}, 80000, nil, 0.05},
		{"1:3:6", map[string]float64{"A": 1, "B": 3, "C": 6}, 60000, nil, 0.05},
		{"extreme", map[string]float64{"hi": 1e6, "lo": 1e-6}, 100,
			map[string]int{"hi": 99, "lo": 1}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			for id, w := range tc.weights {
				s.AddTenant(tenant.New(id, w))
			}
			for i := 0; i < tc.steps; i++ {
				for id := range tc.weights {
					s.Submit(task.Task{Tenant: id, Cost: 1, Seq: uint64(i)})
				}
			}
			got := counts(drain(s, tc.steps))
			for id, tn := range s.tenants {
				if math.IsNaN(tn.VT()) || math.IsInf(tn.VT(), 0) {
					t.Fatalf("tenant %s vt not finite: %v", id, tn.VT())
				}
			}
			if tc.want != nil {
				for id, want := range tc.want {
					if got[id] != want {
						t.Fatalf("tenant %s: got %d, want %d", id, got[id], want)
					}
				}
				return
			}
			var wsum float64
			for _, w := range tc.weights {
				wsum += w
			}
			for id, w := range tc.weights {
				share := float64(got[id]) / float64(tc.steps)
				dev := math.Abs(share-w/wsum) / (w / wsum)
				if dev > tc.tol {
					t.Fatalf("tenant %s share %.4f deviates %.4f from weight share", id, share, dev)
				}
			}
		})
	}
}

func TestIdleRejoin(t *testing.T) {
	build := func() *Scheduler {
		s := New()
		s.AddTenant(tenant.New("X", 1))
		s.AddTenant(tenant.New("Y", 1))
		for i := 0; i < 1000; i++ {
			s.Submit(task.Task{Tenant: "Y", Cost: 1, Seq: uint64(i)})
		}
		drain(s, 1000)
		for i := 0; i < 1500; i++ {
			s.Submit(task.Task{Tenant: "X", Cost: 1, Seq: uint64(i)})
			s.Submit(task.Task{Tenant: "Y", Cost: 1, Seq: uint64(1000 + i)})
		}
		return s
	}
	cases := []struct {
		name    string
		lift    bool
		minRun  int
		maxRunX int
	}{
		{"lifted", true, 0, 3},
		{"not_lifted", false, 900, 1500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := build()
			if !tc.lift {
				for i, tn := range s.h.items {
					if tn.ID == "X" {
						tn.SetVT(0)
						heap.Fix(&s.h, i)
					}
				}
			}
			out := drain(s, 3000)
			run := maxRun(out, "X")
			if run < tc.minRun || run > tc.maxRunX {
				t.Fatalf("X max consecutive run = %d, want [%d,%d]", run, tc.minRun, tc.maxRunX)
			}
			if tc.lift {
				firstY := -1
				for i, tsk := range out {
					if tsk.Tenant == "Y" {
						firstY = i
						break
					}
				}
				if firstY < 0 || firstY > 3 {
					t.Fatalf("Y starved: first Y at step %d", firstY)
				}
			}
		})
	}
}

func TestTieBreak(t *testing.T) {
	cases := []struct {
		name string
		ids  []string
		each int
		want string
	}{
		{"three", []string{"c", "a", "b"}, 1, "abc"},
		{"two", []string{"b", "a"}, 2, "abab"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			for _, id := range tc.ids {
				s.AddTenant(tenant.New(id, 1))
			}
			for i := 0; i < tc.each; i++ {
				for _, id := range tc.ids {
					s.Submit(task.Task{Tenant: id, Cost: 1, Seq: uint64(i)})
				}
			}
			got := ""
			for _, tsk := range drain(s, tc.each*len(tc.ids)) {
				got += tsk.Tenant
			}
			if got != tc.want {
				t.Fatalf("order = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSelectionCost(t *testing.T) {
	cases := []struct{ n, bound int }{{10, 16}, {1000, 40}}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.n), func(t *testing.T) {
			s := New()
			for i := 0; i < tc.n; i++ {
				id := fmt.Sprintf("t%04d", i)
				s.AddTenant(tenant.New(id, 1))
				s.Submit(task.Task{Tenant: id, Cost: 1, Seq: uint64(i)})
				s.Submit(task.Task{Tenant: id, Cost: 1, Seq: uint64(i)})
			}
			for i := 0; i < 500; i++ {
				s.AddTenant(tenant.New(fmt.Sprintf("idle%04d", i), 1))
			}
			maxCmp := 0
			for i := 0; i < 6; i++ {
				s.Next()
				if s.LastComparisons() > maxCmp {
					maxCmp = s.LastComparisons()
				}
			}
			if maxCmp > tc.bound {
				t.Fatalf("comparisons %d exceed bound %d", maxCmp, tc.bound)
			}
			if s.Active() != tc.n {
				t.Fatalf("Active() = %d, want %d (idle tenants must not count)", s.Active(), tc.n)
			}
			drain(s, 2*tc.n)
			if s.Active() != 0 {
				t.Fatalf("Active() = %d after drain, want 0", s.Active())
			}
		})
	}
}

func TestZeroCost(t *testing.T) {
	s := New()
	s.AddTenant(tenant.New("X", 1))
	s.AddTenant(tenant.New("Y", 1))
	for i := 0; i < 100; i++ {
		s.Submit(task.Task{Tenant: "X", Cost: 0, Seq: uint64(i)})
		s.Submit(task.Task{Tenant: "Y", Cost: 1, Seq: uint64(i)})
	}
	out := drain(s, 200)
	if run := maxRun(out, "X"); run > 2 {
		t.Fatalf("zero-cost X monopolized: max run %d", run)
	}
	got := counts(out)
	if got["X"] != 100 || got["Y"] != 100 {
		t.Fatalf("counts = %v, want X=100 Y=100", got)
	}
}

func TestDeterminism(t *testing.T) {
	run := func() []string {
		s := New()
		ids := []string{"a", "b", "c", "d", "e"}
		for i, id := range ids {
			s.AddTenant(tenant.New(id, float64(i+1)))
		}
		for i := 0; i < 1000; i++ {
			s.Submit(task.Task{Tenant: ids[i%5], Cost: float64((i * 37) % 5), Seq: uint64(i)})
		}
		var seq []string
		for _, tsk := range drain(s, 1000) {
			seq = append(seq, fmt.Sprintf("%s:%d", tsk.Tenant, tsk.Seq))
		}
		return seq
	}
	want := run()
	for i := 0; i < 20; i++ {
		got := run()
		if len(got) != len(want) {
			t.Fatalf("run %d: length %d != %d", i, len(got), len(want))
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("run %d differs at %d: %s != %s", i, j, got[j], want[j])
			}
		}
	}
}
