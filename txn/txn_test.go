package txn

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func ok(t *testing.T, c bool, f string, a ...any) {
	t.Helper()
	if !c {
		t.Fatalf(f, a...)
	}
}

func TestThirteenStepSequence(t *testing.T) {
	sentinel := []error{nil, ErrOffsetJump, ErrEffectMissing}
	ops := [][5]int64{
		{'a', 1, 10, 0, 0}, {'c', 1, 0, 1, 0}, {'a', 2, 20, 1, 0},
		{'a', 2, 20, 1, 0}, {'r', 0, 0, 1, 0}, {'a', 2, 20, 1, 0},
		{'c', 2, 0, 2, 0}, {'c', 4, 0, 2, 1}, {'a', 3, 30, 2, 0},
		{'c', 3, 0, 3, 0}, {'c', 4, 0, 3, 2}, {'a', 4, 40, 3, 0},
		{'c', 4, 0, 4, 0},
	}
	q := New()
	for i, o := range ops {
		var err error
		switch o[0] {
		case 'a':
			err = q.Apply(o[1], o[2])
		case 'c':
			err = q.Commit(o[1])
		default:
			q.Restart()
		}
		ok(t, q.Committed() == o[3] && errors.Is(err, sentinel[o[4]]), "step %d C=%d e=%v", i+1, q.Committed(), err)
	}
	want := map[int64]int64{1: 10, 2: 20, 3: 30, 4: 40}
	ok(t, reflect.DeepEqual(q.store.Snapshot(), want), "store=%v", q.store.Snapshot())
}
func TestRestartPending(t *testing.T) {
	q := New()
	ok(t, q.Apply(1, 10) == nil && q.Commit(1) == nil && q.Apply(2, 20) == nil, "setup")
	p := q.Restart()
	ok(t, q.Committed() == 1 && reflect.DeepEqual(p, []int64{2}), "C=%d p=%v", q.Committed(), p)
	v, has := q.store.Get(2)
	ok(t, has && v == 20, "effect lost")
}
func TestCommitCheckCountO1(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		q := New()
		for i := int64(1); i <= m; i++ {
			ok(t, q.Apply(i, i) == nil && q.Commit(i) == nil, "m=%d @%d", m, i)
		}
		ok(t, q.Apply(m+1, m+1) == nil && q.Commit(m+1) == nil && q.lastCheckCount == 1, "m=%d n=%d", m, q.lastCheckCount)
	}
}
func TestIdempotentReplayAgainstNaive(t *testing.T) {
	for _, seed := range []int64{1, 7, 42} {
		q, n := New(), &naiveRef{m: map[int64]int64{}}
		rnd := rand.New(rand.NewSource(seed))
		step := func(a bool, s, v int64) {
			var e1, e2 error
			if a {
				e1, e2 = q.Apply(s, v), n.step(true, s, v)
			} else {
				e1, e2 = q.Commit(s), n.step(false, s, v)
			}
			ok(t, errors.Is(e1, e2) && q.Committed() == n.c && reflect.DeepEqual(q.store.Snapshot(), n.m), "seed=%d %v/%v", seed, e1, e2)
		}
		for i := 0; i < 2000; i++ {
			switch rnd.Intn(7) {
			case 0:
				step(true, q.c+1, rnd.Int63n(999)+1)
			case 1:
				if p := q.Pending(); len(p) > 0 {
					step(true, p[0], rnd.Int63n(999)+1)
				}
			case 2:
				step(true, -1, 1)
			case 3:
				step(true, q.c+2, 1)
			case 4:
				step(false, q.c+1, 0)
			case 5:
				step(false, q.c, 0)
				step(false, q.c+2, 0)
			default:
				q.Restart()
			}
		}
	}
}
func TestErrorsDistinctAndStateUntouched(t *testing.T) {
	fns := [4]func(*Txn) error{
		func(q *Txn) error { return q.Apply(-1, 1) },
		func(q *Txn) error { return q.Apply(3, 3) },
		func(q *Txn) error { return q.Commit(1) },
		func(q *Txn) error { return q.Commit(2) },
	}
	wants := [4]error{ErrInvalidSeq, ErrOutOfOrder, ErrEffectMissing, ErrOffsetJump}
	seen := map[error]bool{}
	for i := range fns {
		q := New()
		err := fns[i](q)
		ok(t, errors.Is(err, wants[i]) && !seen[wants[i]], "err=%v want %v", err, wants[i])
		seen[wants[i]] = true
		ok(t, q.Committed() == 0 && len(q.store.Snapshot()) == 0 && q.Apply(1, 9) == nil && q.Commit(1) == nil && q.Committed() == 1, "trace/unusable")
	}
}
func TestConcurrentDuplicateApply(t *testing.T) {
	q := New()
	ok(t, q.Apply(1, 1) == nil && q.Commit(1) == nil, "setup")
	const N = 64
	start, stop := make(chan struct{}), make(chan struct{})
	var wg, rd sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _ = q.Apply(2, 20) }()
	}
	var regress atomic.Bool
	rd.Add(1)
	go func() {
		defer rd.Done()
		prev := q.Committed()
		for {
			select {
			case <-stop:
				return
			default:
				cur := q.Committed()
				if cur < prev {
					regress.Store(true)
				}
				prev = cur
			}
		}
	}()
	close(start)
	wg.Wait()
	ok(t, q.Commit(2) == nil && q.Committed() == 2 && len(q.Pending()) == 0, "C=%d p=%v", q.Committed(), q.Pending())
	v, has := q.store.Get(2)
	ok(t, has && v == 20, "store[2]")
	close(stop)
	rd.Wait()
	ok(t, !regress.Load(), "Committed decreased")
}
