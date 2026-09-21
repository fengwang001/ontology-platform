package reasm

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/budget"
)

func TestOverBudgetRejectedWithZeroStateChange(t *testing.T) {
	clk := newClock()
	r := New(8, time.Minute, clk.Now)
	if _, _, err := r.Submit("a", 0, []byte("1234"), 8); err != nil {
		t.Fatal(err)
	}
	usedBefore := r.Used()
	stBefore := r.Status("a")
	_, _, err := r.Submit("b", 0, []byte("12345"), 10)
	if !errors.Is(err, budget.ErrExhausted) {
		t.Fatalf("err = %v, want budget.ErrExhausted", err)
	}
	if r.Used() != usedBefore {
		t.Fatalf("used changed: %d -> %d", usedBefore, r.Used())
	}
	if st := r.Status("a"); st != stBefore {
		t.Fatalf("status of a changed: %+v -> %+v", stBefore, st)
	}
	if st := r.Status("b"); st != (Status{}) {
		t.Fatalf("rejected message left state: %+v", st)
	}
	// Duplicate of an already-received fragment must stay free even when
	// the budget is nearly full.
	if _, _, err := r.Submit("a", 0, []byte("1234"), 8); err != nil {
		t.Fatalf("idempotent duplicate should not be charged: %v", err)
	}
}

func TestDeliveredMessageQueryIsZero(t *testing.T) {
	clk := newClock()
	r := New(1<<20, time.Minute, clk.Now)
	if _, done, err := r.Submit("m", 0, []byte("ab"), 2); err != nil || !done {
		t.Fatalf("submit: done=%v err=%v", done, err)
	}
	if st := r.Status("m"); st != (Status{}) {
		t.Fatalf("delivered status = %+v, want zero", st)
	}
	if used := r.Used(); used != 0 {
		t.Fatalf("used = %d, want 0", used)
	}
}

func TestStatusReportsProgressAndRemaining(t *testing.T) {
	clk := newClock()
	r := New(1<<20, 30*time.Second, clk.Now)
	if _, _, err := r.Submit("m", 0, []byte("ab"), 4); err != nil {
		t.Fatal(err)
	}
	clk.Advance(10 * time.Second)
	st := r.Status("m")
	if st.Received != 2 || st.Complete || st.Remaining != 20*time.Second {
		t.Fatalf("status = %+v, want {2 false 20s}", st)
	}
}

func TestConcurrentSingleDeliverer(t *testing.T) {
	clk := newClock()
	r := New(1<<20, time.Minute, clk.Now)
	msg := []byte("concurrent reassembly works!")
	const workers = 16
	var deliveries atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			// Each byte is submitted by exactly one worker, so the
			// message assembles exactly once; the completion
			// transition must still be observed by a single caller.
			for i := w; i < len(msg); i += workers {
				_, done, err := r.Submit("m", i, msg[i:i+1], len(msg))
				if err != nil {
					t.Errorf("submit: %v", err)
					return
				}
				if done {
					deliveries.Add(1)
				}
			}
		}(w)
	}
	wg.Wait()
	if got := deliveries.Load(); got != 1 {
		t.Fatalf("deliveries = %d, want exactly 1", got)
	}
	if used := r.Used(); used != 0 {
		t.Fatalf("used = %d, want 0", used)
	}
}

func TestConcurrentDistinctMessages(t *testing.T) {
	clk := newClock()
	r := New(1<<20, time.Minute, clk.Now)
	var wg sync.WaitGroup
	var deliveries atomic.Int64
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := string(rune('a' + g))
			if _, _, err := r.Submit(id, 1, []byte("b"), 2); err != nil {
				t.Errorf("part 1: %v", err)
				return
			}
			_, done, err := r.Submit(id, 0, []byte("a"), 2)
			if err != nil {
				t.Errorf("part 0: %v", err)
				return
			}
			if done {
				deliveries.Add(1)
			}
		}(g)
	}
	wg.Wait()
	if got := deliveries.Load(); got != 32 {
		t.Fatalf("deliveries = %d, want 32", got)
	}
}
