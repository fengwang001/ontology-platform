package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"

	"ontology/check"
	"ontology/sem"
	"ontology/waitq"
)

var fails int

func ok(name string, cond bool) {
	if !cond {
		fails++
	}
	fmt.Println(map[bool]string{true: "OK ", false: "FAIL "}[cond] + name)
}
func waitFor(f func() bool) {
	for i := 0; i < 1e7 && !f(); i++ {
		runtime.Gosched()
	}
}
func waiter(s *sem.Sem, n int64, grant chan struct{}) {
	go func() {
		if s.Acquire(context.Background(), n) == nil {
			grant <- struct{}{}
		}
	}()
}
func main() {
	bg := context.Background()
	s := sem.New(10, 2)
	ok("too large rejected", errors.Is(s.Acquire(bg, 11), sem.ErrTooLarge) && !s.TryAcquire(11))
	s.TryAcquire(4)
	ok("over release keeps ledger", errors.Is(s.Release(5), sem.ErrOverRelease) && s.Stats().Used == 4)
	m := check.New(s)
	m.Acquire(bg, 4) // used 8, avail 2
	headCtx, headCancel := context.WithCancel(bg)
	headErr := make(chan error, 1)
	go func() { headErr <- m.Acquire(headCtx, 8) }()
	waitFor(func() bool { return s.Stats().Waiters == 1 })
	ok("fifo: no queue jump", !s.TryAcquire(2))
	grant := make(chan struct{}, 4)
	waiter(s, 2, grant)
	waitFor(func() bool { return s.Stats().Waiters == 2 })
	ok("waiter limit", errors.Is(s.Acquire(bg, 1), sem.ErrTooManyWaiters))
	headCancel() // DESIGN.md derivation 1
	ok("head cancel wakes follower", errors.Is(<-headErr, context.Canceled) && s.Stats().Used == 10)
	waiter(s, 2, grant)
	waiter(s, 2, grant)
	waitFor(func() bool { return s.Stats().Waiters == 2 })
	m.Release(4)
	ok("group wake in order", s.Stats().WakeChecked == 2 && s.Stats().WakeGranted == 2)
	_, _, _ = <-grant, <-grant, <-grant
	s.Release(10)
	ok("cancel clean and settled", m.Settled())
	ok("cancel/grant race conserves quota", raceCheck())
	wqOK, drainOK := complexityCheck()
	ok("waitq cancel O(1)", wqOK)
	ok("release drain linear", drainOK)
	total := "OK total"
	if fails > 0 {
		total = "FAIL total"
	}
	fmt.Println(total)
	os.Exit(min(fails, 1))
}
func raceCheck() bool {
	for range 2000 {
		s := sem.New(10, 4)
		s.Acquire(context.Background(), 10)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- s.Acquire(ctx, 5) }()
		waitFor(func() bool { return s.Stats().Waiters == 1 })
		start, fin := make(chan struct{}), make(chan struct{}, 2)
		go func() { <-start; cancel(); fin <- struct{}{} }()
		go func() { <-start; s.Release(5); fin <- struct{}{} }()
		close(start)
		_, _ = <-fin, <-fin
		if <-done == nil {
			s.Release(5)
		}
		s.Release(5)
		if st := s.Stats(); st.Used != 0 || st.Waiters != 0 {
			return false
		}
	}
	return true
}
func complexityCheck() (wqOK, drainOK bool) {
	wqOK, drainOK = true, true
	for _, scale := range []int{100, 100000} {
		q := waitq.New()
		ws := make([]waitq.Waiter, scale)
		for i := range ws {
			q.Push(&ws[i])
		}
		q.Remove(&ws[scale/2])
		wqOK = wqOK && q.LastCancelChecked() <= 2
	}
	for _, scale := range []int{100, 10000} {
		s := sem.New(int64(scale), scale)
		s.TryAcquire(int64(scale))
		grant := make(chan struct{}, scale)
		for range scale {
			waiter(s, 1, grant)
		}
		waitFor(func() bool { return s.Stats().Waiters == scale })
		s.Release(int64(scale))
		drainOK = drainOK && s.Stats().WakeChecked <= s.Stats().WakeGranted+1 && s.Stats().WakeGranted == scale
		drainOK = drainOK && s.Release(int64(scale)) == nil && s.Stats().Used == 0
	}
	return
}
