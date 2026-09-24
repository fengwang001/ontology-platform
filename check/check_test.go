package check_test

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/check"
	"ontology/sem"
	"ontology/waitq"
)

func TestSemantics(t *testing.T) {
	cases := map[string]func(*testing.T){
		"fast errors": func(t *testing.T) {
			s, _ := check.NewSem(10, 0)
			check.Require(t, errors.Is(s.Acquire(context.Background(), 11), sem.ErrTooLarge) && !s.TryAcquire(11), "n>Cap")
			s.TryAcquire(4)
			check.Require(t, errors.Is(s.Release(5), sem.ErrOverRelease) && s.Stats().Used == 4, "over release")
			s.TryAcquire(6)
			check.Require(t, errors.Is(s.Acquire(context.Background(), 1), sem.ErrTooManyWaiters), "waiter limit")
		},
		"fifo and group wake": func(t *testing.T) {
			s, m := check.NewSem(10, 8)
			m.Acquire(context.Background(), 10)
			cs, rel, wg := check.Spawn(t, m, s, 3, 2, 3)
			check.Require(t, !s.TryAcquire(1), "queue jump")
			m.Release(4)
			check.Require(t, s.Stats().WakeChecked == 3 && s.Stats().WakeGranted == 2 && s.Stats().Used == 10, "group wake")
			check.Settle(t, m, cs, rel, wg, 6)
		},
		"head cancel wakes": func(t *testing.T) { // pins DESIGN.md derivation 1
			s, m := check.NewSem(10, 4)
			m.Acquire(context.Background(), 8)
			ctx, cancel := context.WithCancel(context.Background())
			headErr := make(chan error, 1)
			go func() { headErr <- m.Acquire(ctx, 8) }()
			check.WaitFor(t, func() bool { return s.Stats().Waiters == 1 })
			cs, rel, wg := check.Spawn(t, m, s, 1, 2, 2)
			cancel()
			check.Require(t, errors.Is(<-headErr, context.Canceled) && s.Stats().Used == 10, "cancel must wake")
			check.Settle(t, m, cs, rel, wg, 8)
		},
	}
	for name, fn := range cases {
		t.Run(name, fn)
	}
}
func TestComplexity(t *testing.T) {
	for _, scale := range []int{100, 100000} {
		q := waitq.New()
		ws := make([]waitq.Waiter, scale)
		for i := range ws {
			q.Push(&ws[i])
		}
		q.Remove(&ws[scale/2])
		check.Require(t, q.LastCancelChecked() <= 2 && q.Len() == scale-1, "cancel not O(1)")
	}
	for _, scale := range []int{100, 10000} {
		s, m := check.NewSem(int64(scale), scale)
		s.TryAcquire(int64(scale))
		cs, rel, wg := check.Spawn(t, m, s, scale, 1, scale)
		s.Release(int64(scale))
		check.Require(t, s.Stats().WakeChecked <= s.Stats().WakeGranted+1 && s.Stats().WakeGranted == scale, "drain cost")
		check.Settle(t, m, cs, rel, wg, 0)
	}
}
func TestFaultInjection(t *testing.T) {
	type fault func([]context.CancelFunc, *check.Model) int64
	cancelAt := func(i int) fault { return func(cs []context.CancelFunc, m *check.Model) int64 { cs[i](); return 0 } }
	race := func(cs []context.CancelFunc, m *check.Model) int64 { // cancel aligned with Release
		start, done := make(chan struct{}), make(chan struct{}, 2)
		go func() { <-start; cs[0](); done <- struct{}{} }()
		go func() { <-start; m.Release(2); done <- struct{}{} }()
		close(start)
		_, _ = <-done, <-done
		return 2
	}
	names := []string{"head", "middle", "tail", "cancel+release"}
	rounds := []int{500, 500, 500, 5000}
	faults := []fault{cancelAt(0), cancelAt(1), cancelAt(2), race}
	for i, name := range names {
		t.Run(name, func(t *testing.T) {
			for r := 0; r < rounds[i]; r++ {
				s, m := check.NewSem(10, 8)
				m.Acquire(context.Background(), 10)
				cs, rel, wg := check.Spawn(t, m, s, 3, 2, 3)
				check.Settle(t, m, cs, rel, wg, 10-faults[i](cs, m))
			}
		})
	}
}
func TestConcurrent(t *testing.T) {
	_, m := check.NewSem(64, 1<<20)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				ctx, cancel := context.WithCancel(context.Background())
				if r.Intn(4) == 0 {
					cancel()
				}
				if w := 1 + r.Int63n(8); m.Acquire(ctx, w) == nil {
					m.Release(w)
				}
				cancel()
				check.Require(t, m.Consistent(), "diverged")
			}
		}()
	}
	wg.Wait()
	check.Require(t, m.Settled(), "not settled")
}
