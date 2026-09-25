package check

import (
	"errors"
	"fmt"
	"ontology/queue"
	"sync"
	"testing"
)

func TestFIFO(t *testing.T) {
	cases := []struct{ name string; n int; alt bool }{
		{"five", 5, false}, {"large", 10000, false}, {"alternating", 10000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, r := queue.New[int](), NewReference[int]()
			for i := 0; i < tc.n; i++ {
				q.Enqueue(i); r.Enqueue(i)
				if tc.alt && !EqualStep(q, r) { t.Fatal(ErrFIFOMismatch) }
			}
			for i := 0; i < tc.n && !tc.alt; i++ {
				if !EqualStep(q, r) { t.Fatal(ErrFIFOMismatch) }
			}
		})
	}
	t.Run("empty", func(t *testing.T) {
		q := queue.New[int]()
		if v, ok := q.Dequeue(); ok || v != 0 { t.Fatal(ErrEmptyQueue) }
		if v, ok := q.Peek(); ok || v != 0 { t.Fatal(ErrEmptyQueue) }
	})
}

func TestTransfers(t *testing.T) {
	for _, n := range []int{10, 20} {
		t.Run(fmt.Sprintf("n-%d", n), func(t *testing.T) {
			q, bad := queue.New[int](), &BadQueue{}
			for i := 0; i < n; i++ { q.Enqueue(i); bad.Add(i) }
			q.Dequeue()
			if q.LastMoves() != n { t.Fatal("missing full transfer") }
			q.Dequeue()
			if q.LastMoves() != 0 { t.Fatal("unexpected transfer") }
			if v, ok := bad.Get(); !ok || v != 0 || bad.Moves < n {
				t.Fatalf("bad queue v=%d ok=%v moves=%d", v, ok, bad.Moves)
			}
		})
	}
	t.Run("amortized-budget", func(t *testing.T) {
		_, _, done, moves := RandomWorkload(1)
		if done != 10000 { t.Fatal(ErrFIFOMismatch) }
		if moves > 2*done { t.Fatal(ErrAmortizedBudget) }
	})
	t.Run("sentinels", func(t *testing.T) {
		es := []error{ErrEmptyQueue, ErrFIFOMismatch, ErrAmortizedBudget}
		for _, e := range es {
			if !errors.Is(fmt.Errorf("%w", e), e) { t.Fatal(e) }
		}
	})
}

func TestConcurrentReaders(t *testing.T) {
	q := queue.New[int]()
	for i := 0; i < 32; i++ { q.Enqueue(i) }
	all := make([][2]int, 16)
	var wg sync.WaitGroup
	for i := range all {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, ok := q.Peek()
			flag := 0
			if ok { flag = 1 }
			all[i] = [2]int{v, flag}
		}(i)
	}
	wg.Wait()
	for _, got := range all[1:] {
		if got != all[0] { t.Fatal("inconsistent readers") }
	}
}
