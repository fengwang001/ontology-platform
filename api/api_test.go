package api_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

// Invariant 2: a resource has at most one holder at any time.
func TestMutex(t *testing.T) {
	m := api.New()
	m.AddTask("hi", 5)
	m.AddTask("lo", 3)
	m.AddResource("R")
	_ = m.Use("hi", "R")
	_ = m.Use("lo", "R")
	steps := []struct {
		run   func() (bool, error)
		want  bool
		wantE error
	}{
		{func() (bool, error) { return m.Acquire("lo", "R") }, true, nil},  // lo holds R
		{func() (bool, error) { return m.Acquire("hi", "R") }, false, nil}, // blocked, no 2nd holder
		{func() (bool, error) { return m.Acquire("lo", "R") }, false, api.ErrAlreadyHeld},
		{func() (bool, error) { return m.Acquire("hi", "R") }, false, nil}, // still blocked
		{func() (bool, error) { return false, m.Release("lo", "R") }, false, nil},
		{func() (bool, error) { return m.Acquire("hi", "R") }, true, nil}, // now hi holds R
	}
	for i, s := range steps {
		got, err := s.run()
		if got != s.want || err != s.wantE {
			t.Fatalf("step %d: got %v,%v want %v,%v", i, got, err, s.want, s.wantE)
		}
	}
}

// Invariant 3: ceiling == max user priority; holder is boosted to it.
func TestCeilingAndBoost(t *testing.T) {
	for i, users := range [][]int{{5, 1}, {3, 1}, {2, 4, 3}, {7}} {
		m := api.New()
		m.AddResource("R")
		want := 0
		for j, p := range users {
			tk := fmt.Sprintf("u%d", j)
			m.AddTask(tk, p)
			_ = m.Use(tk, "R")
			want = max(want, p)
		}
		last := fmt.Sprintf("u%d", len(users)-1)
		if ok, err := m.Acquire(last, "R"); !ok || err != nil {
			t.Fatalf("case %d: acquire: %v %v", i, ok, err)
		}
		if got := m.SystemCeiling(); got != want {
			t.Fatalf("case %d: ceiling %d want %d", i, got, want)
		}
		if eff, _ := m.EffectivePriority(last); eff != want {
			t.Fatalf("case %d: effective %d want %d", i, eff, want)
		}
	}
}

// Invariant 4: rejected ops map to distinct sentinels, leave state
// unchanged, and the manager stays usable.
func TestFaultInjection(t *testing.T) {
	errs := []error{api.ErrUnknownTask, api.ErrUnknownResource, api.ErrAlreadyHeld, api.ErrNotHeld}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				t.Fatal("sentinel errors are not mutually distinct")
			}
		}
	}
	m := api.New()
	m.AddTask("a", 5)
	m.AddTask("b", 2)
	m.AddResource("R")
	_ = m.Use("a", "R")
	_, _ = m.Acquire("a", "R") // a holds R, system ceiling 5
	cases := []struct {
		run  func() error
		want error
	}{
		{func() error { _, e := m.Acquire("ghost", "R"); return e }, api.ErrUnknownTask},
		{func() error { _, e := m.Acquire("a", "nope"); return e }, api.ErrUnknownResource},
		{func() error { _, e := m.Acquire("a", "R"); return e }, api.ErrAlreadyHeld},
		{func() error { return m.Release("b", "R") }, api.ErrNotHeld},
		{func() error { return m.Release("ghost", "R") }, api.ErrUnknownTask},
		{func() error { return m.Release("a", "nope") }, api.ErrUnknownResource},
		{func() error { return m.Use("ghost", "R") }, api.ErrUnknownTask},
		{func() error { return m.Use("a", "nope") }, api.ErrUnknownResource},
	}
	for i, c := range cases {
		if err := c.run(); err != c.want {
			t.Errorf("case %d: got %v want %v", i, err, c.want)
		}
		if got := m.SystemCeiling(); got != 5 {
			t.Fatalf("case %d changed system ceiling to %d", i, got)
		}
	}
	if err := m.Release("a", "R"); err != nil { // still usable afterwards
		t.Fatalf("unusable after faults: %v", err)
	}
}

// Concurrency: pairs of goroutines contend per resource; mutual
// exclusion must hold continuously (atomic counters), no sleeps.
func TestConcurrent(t *testing.T) {
	c := api.New()
	const np = 4
	held, bad := [np]int32{}, int32(0)
	for i := 0; i < 2*np; i++ {
		tk, r := fmt.Sprintf("c%d", i), fmt.Sprintf("cr%d", i%np)
		c.AddTask(tk, 10+i)
		c.AddResource(r)
		_ = c.Use(tk, r)
	}
	var wg sync.WaitGroup
	for i := 0; i < 2*np; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tk, r := fmt.Sprintf("c%d", i), fmt.Sprintf("cr%d", i%np)
			for range 3000 {
				_, _ = c.EffectivePriority(tk)
				_ = c.SystemCeiling()
				if ok, _ := c.Acquire(tk, r); ok {
					if atomic.AddInt32(&held[i%np], 1) != 1 {
						atomic.AddInt32(&bad, 1)
					}
					atomic.AddInt32(&held[i%np], -1)
					_ = c.Release(tk, r)
				}
			}
		}(i)
	}
	wg.Wait()
	if bad != 0 || c.SystemCeiling() != 0 {
		t.Fatalf("bad=%d ceiling=%d", bad, c.SystemCeiling())
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
