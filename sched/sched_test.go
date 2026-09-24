package sched

import (
	"context"
	"errors"
	"math"
	"testing"

	"ontology/task"
	"ontology/tenant"
)

func load(t *testing.T, s *Scheduler, weights map[string]float64, each int) {
	t.Helper()
	ctx := context.Background()
	for id, w := range weights {
		if err := s.Add(id, w); err != nil {
			t.Fatal(err)
		}
		for range each {
			if _, err := s.Submit(ctx, id, 1); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func run(s *Scheduler, n int) []task.Task {
	out := make([]task.Task, 0, n)
	for range n {
		j, ok := s.TryNext()
		if !ok {
			break
		}
		out = append(out, j)
	}
	return out
}

func TestValidationErrors(t *testing.T) {
	badWeights := []float64{0, -1, math.NaN(), math.Inf(1), 1e-12, 1e12}
	for _, w := range badWeights {
		s := New(nil)
		if err := s.Add("a", w); !errors.Is(err, tenant.ErrBadWeight) {
			t.Fatalf("weight %v: want ErrBadWeight, got %v", w, err)
		}
	}
	ctx := context.Background()
	s := New(nil)
	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"submit unknown", func() error { _, e := s.Submit(ctx, "x", 1); return e }, ErrUnknownTenant},
		{"duplicate add", func() error { return s.Add("a", 1) }, nil},
		{"duplicate add2", func() error { return s.Add("a", 1) }, ErrExists},
		{"bad cost", func() error { _, e := s.Submit(ctx, "a", -1); return e }, task.ErrBadCost},
		{"remove unknown", func() error { _, e := s.Remove("zz"); return e }, ErrUnknownTenant},
	}
	for _, c := range cases {
		if err := c.fn(); !errors.Is(err, c.want) {
			t.Fatalf("%s: want %v, got %v", c.name, c.want, err)
		}
	}
}

func TestWeightedShare(t *testing.T) {
	cases := []struct {
		name    string
		weights map[string]float64
		each    int
		pick    int
		tol     float64
	}{
		{"1:3", map[string]float64{"a": 1, "b": 3}, 40000, 40000, 0.05},
		{"1:3:6@100k", map[string]float64{"a": 1, "b": 3, "c": 6}, 100000, 100000, 0.05},
	}
	ctx := context.Background()
	for _, c := range cases {
		s := New(nil)
		load(t, s, c.weights, c.each)
		var totalW float64
		for _, w := range c.weights {
			totalW += w
		}
		got := map[string]int{}
		for _, j := range run(s, c.pick) {
			got[j.Tenant]++
		}
		for id, w := range c.weights {
			want := float64(c.pick) * w / totalW
			rel := math.Abs(float64(got[id])-want) / want
			if rel >= c.tol {
				t.Fatalf("%s tenant %s: got %d want %.0f rel %.3f", c.name, id, got[id], want, rel)
			}
		}
		_ = ctx
	}
}

// miniSim models vt scheduling with a switchable idle-rejoin lift, for FINDINGS.
type miniSim struct{ vt, queued float64 }

func rejoinRun(lift bool) int {
	x, y := miniSim{}, miniSim{vt: 1000, queued: 1000}
	x.queued = 1000
	if lift {
		x.vt = 1000
	}
	streak := 0
	for range 2000 {
		xFirst := x.vt < y.vt || (x.vt == y.vt)
		if xFirst && x.queued > 0 {
			x.vt++
			x.queued--
			streak++
		} else if y.queued > 0 {
			y.vt++
			y.queued--
			if streak > 0 {
				break
			}
		}
	}
	return streak
}

func TestIdleRejoin(t *testing.T) {
	if got := rejoinRun(false); got != 1000 {
		t.Fatalf("no-lift expected 1000 consecutive X, got %d", got)
	}
	if got := rejoinRun(true); got != 1 {
		t.Fatalf("lift expected 1 consecutive X, got %d", got)
	}
	ctx := context.Background()
	s := New(nil)
	load(t, s, map[string]float64{"Y": 1}, 2000)
	run(s, 1000)
	if err := s.Add("X", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(ctx, "X", 1); err != nil {
		t.Fatal(err)
	}
	for step := range 5 {
		j, ok := s.TryNext()
		if !ok {
			t.Fatal("scheduler drained")
		}
		if step == 0 && j.Tenant != "X" {
			t.Fatalf("X not served first after rejoin: %s", j.Tenant)
		}
		if step > 0 && j.Tenant != "Y" {
			t.Fatalf("Y starved after X rejoin at step %d", step)
		}
	}
}

func TestTieBreakAndDeterminism(t *testing.T) {
	seq := func() []string {
		s := New(nil)
		load(t, s, map[string]float64{"b": 1, "a": 1, "c": 1}, 3)
		out := []string{}
		for _, j := range run(s, 9) {
			out = append(out, j.Tenant)
		}
		return out
	}
	first := seq()
	wantID := "abc"
	for i, got := range first {
		if got[:1] != wantID[i%3:i%3+1] {
			t.Fatalf("tie order wrong: %v", first)
		}
	}
	for range 20 {
		got := seq()
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("non-deterministic at %d", i)
			}
		}
	}
}

func TestHeapComparisonsAndLaziness(t *testing.T) {
	const n = 1000
	bound := 4 * int(math.Ceil(math.Log2(n)))
	s := New(nil)
	for i := range n {
		id := "t" + pad(i)
		if err := s.Add(id, 1); err != nil {
			t.Fatal(err)
		}
	}
	if s.HeapLen() != 0 {
		t.Fatalf("idle tenants must not occupy heap: %d", s.HeapLen())
	}
	ctx := context.Background()
	for i := range n {
		if _, err := s.Submit(ctx, "t"+pad(i), 1); err != nil {
			t.Fatal(err)
		}
	}
	if s.HeapLen() != n {
		t.Fatalf("heap size %d want %d", s.HeapLen(), n)
	}
	for i := range n {
		if _, ok := s.TryNext(); !ok {
			t.Fatal("drain failed")
		}
		if s.LastComparisons() > bound {
			t.Fatalf("step %d comparisons %d > bound %d", i, s.LastComparisons(), bound)
		}
		if s.HeapLen() != n-1-i {
			t.Fatalf("heap size %d want %d", s.HeapLen(), n-1-i)
		}
	}
}

func TestZeroCostNoHog(t *testing.T) {
	ctx := context.Background()
	s := New(nil)
	if err := s.Add("A", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("B", 1); err != nil {
		t.Fatal(err)
	}
	for range 5000 {
		if _, err := s.Submit(ctx, "A", 0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Submit(ctx, "B", 1); err != nil {
			t.Fatal(err)
		}
	}
	maxRun, streak, bCount := 0, 0, 0
	for _, j := range run(s, 10000) {
		if j.Tenant == "A" {
			streak++
			if streak > maxRun {
				maxRun = streak
			}
		} else {
			streak = 0
			bCount++
		}
	}
	if maxRun > int(1/tenant.MinVCost)+1 || bCount == 0 {
		t.Fatalf("zero-cost hog: maxRun=%d bCount=%d", maxRun, bCount)
	}
}

func TestRemoveTenant(t *testing.T) {
	s := New(nil)
	load(t, s, map[string]float64{"a": 1, "b": 1}, 3)
	tasks, err := s.Remove("a")
	if err != nil || len(tasks) != 3 {
		t.Fatalf("remove: %v tasks=%d", err, len(tasks))
	}
	if _, err := s.Submit(context.Background(), "a", 1); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("submit after remove: %v", err)
	}
	if s.HeapLen() != 1 {
		t.Fatalf("other tenant disturbed: heap=%d", s.HeapLen())
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func pad(i int) string {
	id := itoa(int64(i))
	for len(id) < 4 {
		id = "0" + id
	}
	return id
}
