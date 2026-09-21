package mux

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCloseDrainsWaiters(t *testing.T) {
	clk := newFakeClock()
	m := New(clk.Now)
	ch, err := m.Register("a")
	if err != nil {
		t.Fatal(err)
	}
	waitErr := make(chan error, 1)
	go func() {
		_, err := m.Wait("b", clk.Now().Add(time.Hour))
		waitErr <- err
	}()
	waitPending(t, m, 2)
	m.Close()
	if _, ok := <-ch; ok {
		t.Fatal("channel of closed waiter should be closed without a value")
	}
	if err := <-waitErr; !errors.Is(err, ErrClosed) {
		t.Fatalf("Wait err=%v, want ErrClosed", err)
	}
	if s := m.Stats(); s.Pending != 0 {
		t.Fatalf("Pending=%d after Close, want 0", s.Pending)
	}
	if _, err := m.Register("c"); !errors.Is(err, ErrClosed) {
		t.Fatalf("Register after Close err=%v, want ErrClosed", err)
	}
	if _, err := m.Wait("c", clk.Now()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Wait after Close err=%v, want ErrClosed", err)
	}
	m.Deliver("a", []byte("x"))     // seen id -> Late
	m.Deliver("never", []byte("x")) // unknown id -> Orphans
	s := m.Stats()
	if s.Late != 1 || s.Orphans != 1 {
		t.Fatalf("stats %+v, want Late=1 Orphans=1", s)
	}
	m.Close() // idempotent, must not panic
}

func TestConservationAfterMixedOps(t *testing.T) {
	clk := newFakeClock()
	m := New(clk.Now)
	const registered = 10
	var wg sync.WaitGroup
	// 4 will be delivered, 3 will time out, 3 will be closed.
	for i := 0; i < 4; i++ {
		if _, err := m.Register(fmt.Sprintf("d%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	results := make([]error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = m.Wait(fmt.Sprintf("w%d", i), clk.Now().Add(time.Minute))
		}(i)
	}
	waitPending(t, m, registered)
	for i := 0; i < 4; i++ {
		m.Deliver(fmt.Sprintf("d%d", i), []byte("ok"))
	}
	clk.Advance(time.Minute) // expires w0..w2's window only via Tick below
	m.Tick()                 // all 6 Waiters share the deadline; re-register 3
	wg.Wait()
	for i := 0; i < 6; i++ {
		if !errors.Is(results[i], ErrTimedOut) {
			t.Fatalf("waiter %d err=%v, want ErrTimedOut", i, results[i])
		}
	}
	// Re-register 3 ids and close them instead.
	for i := 0; i < 3; i++ {
		if _, err := m.Register(fmt.Sprintf("c%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	m.Close()
	m.mu.Lock()
	timedOut, closedOut := m.timedOut, m.closedOut
	m.mu.Unlock()
	s := m.Stats()
	completed := s.Delivered + timedOut + closedOut
	total := registered + 3 // 10 initial + 3 re-registered
	if completed != total {
		t.Fatalf("Delivered(%d)+timedOut(%d)+closedOut(%d)=%d, want %d",
			s.Delivered, timedOut, closedOut, completed, total)
	}
	if s.Pending != total-completed {
		t.Fatalf("Pending=%d, want %d", s.Pending, total-completed)
	}
}

func TestConcurrentUse(t *testing.T) {
	m := New(nil) // real clock; deadlines far in the future
	const n = 64
	deadline := time.Now().Add(time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("id-%d", i)
		want := []byte(fmt.Sprintf("payload-%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := m.Wait(id, deadline)
			if err != nil {
				t.Errorf("Wait(%s): %v", id, err)
				return
			}
			if string(got) != string(want) {
				t.Errorf("Wait(%s)=%q, want %q", id, got, want)
			}
		}()
	}
	// Deliver only after every waiter registered, so no response is
	// dropped as an orphan and the Wait goroutines cannot block forever.
	waitPending(t, m, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("id-%d", i)
		want := []byte(fmt.Sprintf("payload-%d", i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Deliver(id, want)
			m.Deliver(id, want) // duplicate: must not be delivered twice
		}()
	}
	// Concurrent Tick/Stats/orphan traffic to shake out races.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				m.Tick()
				_ = m.Stats()
				m.Deliver(fmt.Sprintf("orphan-%d-%d", i, j), nil)
			}
		}(i)
	}
	wg.Wait()
	m.Close()
	s := m.Stats()
	if s.Delivered != n {
		t.Fatalf("Delivered=%d, want %d", s.Delivered, n)
	}
	if s.Late != n {
		t.Fatalf("Late=%d, want %d (duplicate deliveries)", s.Late, n)
	}
	if s.Orphans != 200 {
		t.Fatalf("Orphans=%d, want 200", s.Orphans)
	}
	if s.Pending != 0 {
		t.Fatalf("Pending=%d, want 0", s.Pending)
	}
}
