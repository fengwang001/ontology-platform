package api_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/lock"
)

// TestInheritance: a higher-static waiter sets the effective priority.
func TestInheritance(t *testing.T) {
	cases := []struct{ hprio, w1, w2, want int }{
		{1, 2, 9, 9},   // higher waiter dominates
		{5, 3, 4, 5},   // no higher waiter: stays static
		{7, 20, 6, 20}, // non-head waiter is highest
	}
	for _, tc := range cases {
		m := api.New()
		_ = m.Acquire(1, tc.hprio)
		_ = m.Acquire(2, tc.w1)
		_ = m.Acquire(3, tc.w2)
		if got := m.Effective(); got != tc.want {
			t.Errorf("%+v: effective=%d", tc, got)
		}
	}
}

// TestHandoff: Release hands the lock to the highest-static waiter.
func TestHandoff(t *testing.T) {
	cases := []struct {
		prios []int
		want  int
	}{ // want = id (index+1) of highest-static waiter
		{[]int{2, 9, 5}, 2},
		{[]int{8, 3, 1}, 1},
		{[]int{1, 3, 7, 4}, 3},
	}
	for _, tc := range cases {
		m := api.New()
		_ = m.Acquire(99, 1)
		for i, p := range tc.prios {
			_ = m.Acquire(i+1, p)
		}
		_ = m.Release(99)
		if h, _ := m.Holder(); h != tc.want {
			t.Errorf("%v: holder=%d want %d", tc.prios, h, tc.want)
		}
	}
}

// TestRejectLeavesNoTrace: each fault maps to its own sentinel error,
// and a rejected call changes neither holder nor effective priority.
func TestRejectLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name string
		op   func(*api.Mutex) error
		want error
	}{{"zero priority", func(m *api.Mutex) error { return m.Acquire(50, 0) }, lock.ErrInvalidPriority},
		{"negative priority", func(m *api.Mutex) error { return m.Acquire(51, -3) }, lock.ErrInvalidPriority},
		{"duplicate holder", func(m *api.Mutex) error { return m.Acquire(1, 5) }, lock.ErrDuplicateAcquire},
		{"duplicate waiter", func(m *api.Mutex) error { return m.Acquire(2, 5) }, lock.ErrDuplicateAcquire},
		{"release non-holder", func(m *api.Mutex) error { return m.Release(77) }, lock.ErrNotHolder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := api.New()
			if err := m.Acquire(1, 3); err != nil {
				t.Fatal(err)
			}
			if err := m.Acquire(2, 8); err != nil {
				t.Fatal(err)
			}
			h, _ := m.Holder()
			e := m.Effective()
			if err := tc.op(m); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			if h2, _ := m.Holder(); h2 != h || m.Effective() != e {
				t.Fatal("rejected op changed state")
			}
			if err := m.Release(1); err != nil { // still usable
				t.Fatalf("lock unusable after reject: %v", err)
			}
			if h2, _ := m.Holder(); h2 != 2 {
				t.Fatalf("holder=%d want 2", h2)
			}
		})
	}
	d := lock.ErrDuplicateAcquire // the three sentinels are mutually distinct
	if lock.ErrInvalidPriority == d || d == lock.ErrNotHolder || lock.ErrNotHolder == lock.ErrInvalidPriority {
		t.Fatal("sentinel errors not distinct")
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentAcquire: N goroutines acquire distinct ids on one mutex.
func TestConcurrentAcquire(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		m := api.New()
		var wg sync.WaitGroup
		var inFlight, peak atomic.Int64
		start, done := make(chan struct{}), make(chan struct{})
		go func() { // hammer Holder/Effective concurrently, no sleeps
			for {
				select {
				case <-done:
					return
				default:
					m.Holder()
					m.Effective()
				}
			}
		}()
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				<-start
				cur := inFlight.Add(1)
				if p := peak.Load(); cur > p {
					peak.CompareAndSwap(p, cur)
				}
				if err := m.Acquire(id, id); err != nil { // prio = id: distinct, max = n
					t.Errorf("acquire %d: %v", id, err)
				}
				inFlight.Add(-1)
			}(i + 1)
		}
		close(start)
		wg.Wait()
		close(done)
		if _, ok := m.Holder(); !ok {
			t.Errorf("n=%d: no holder", n)
		}
		if got := m.Effective(); got != n {
			t.Errorf("n=%d: effective=%d want %d", n, got, n)
		}
		if peak.Load() > int64(n) {
			t.Errorf("n=%d: peak in-flight %d exceeds N", n, peak.Load())
		}
	}
}
