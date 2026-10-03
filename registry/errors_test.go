package registry

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestExpiryAndFinishErrors(t *testing.T) {
	r, _ := New(100, 10)
	id, p := []byte("id"), []byte("p")
	run, _ := r.Start(id, p, 2, 0, 0)
	r.Finish(id, run, stCompleted, 10)
	if n, _ := r.Count(109); n != 1 {
		t.Fatalf("count 109=%d want 1", n)
	}
	if n, _ := r.Count(110); n != 0 {
		t.Fatalf("count 110=%d want 0", n)
	}
	if err := r.Finish(id, run, stFailed, 110); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired finish err=%v", err)
	}

	r2, _ := New(100, 10)
	run, _ = r2.Start(id, p, 2, 0, 0)
	if err := r2.Finish([]byte("x"), run, stCompleted, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("notfound err=%v", err)
	}
	if err := r2.Finish(id, run+9, stCompleted, 1); !errors.Is(err, ErrStale) {
		t.Fatalf("stale err=%v", err)
	}
	for _, bad := range []int{-1, stTerminated, 99} {
		if err := r2.Finish(id, run, bad, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad state %d err=%v", bad, err)
		}
	}
	r2.Finish(id, run, stCompleted, 1)
	if err := r2.Finish(id, run, stFailed, 2); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("double finish err=%v", err)
	}
	cases := []struct {
		id, p           []byte
		reuse, conflict int
		now             int64
		want            error
	}{
		{nil, p, 2, 0, 3, ErrInvalid},
		{id, nil, 2, 0, 3, ErrInvalid},
		{id, p, 9, 0, 3, ErrInvalid},
		{id, p, 2, 9, 3, ErrInvalid},
		{id, p, 2, 0, -1, ErrInvalid},
		{[]byte("z"), p, 2, 0, 0, ErrClock},
	}
	for _, tc := range cases {
		if _, err := r2.Start(tc.id, tc.p, tc.reuse, tc.conflict, tc.now); !errors.Is(err, tc.want) {
			t.Fatalf("case=%+v err=%v", tc, err)
		}
	}
	if _, err := New(0, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal("bad R")
	}
	if _, err := New(1, 1_000_001); !errors.Is(err, ErrInvalid) {
		t.Fatal("bad N")
	}
}

func TestConcurrentStart(t *testing.T) {
	for _, conflict := range []int{0, 1} {
		t.Run(fmt.Sprintf("conflict=%d", conflict), func(t *testing.T) {
			r, _ := New(1000, 1000)
			id := []byte("id")
			var wg sync.WaitGroup
			res := make(chan int64, 100)
			errs := make(chan error, 100)
			for i := 0; i < 100; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					v, e := r.Start(id, []byte("owner"), 2, conflict, 0)
					res <- v
					errs <- e
				}()
			}
			wg.Wait()
			close(res)
			close(errs)
			success, running, first := 0, 0, int64(0)
			for e := range errs {
				switch {
				case e == nil:
					success++
				case errors.Is(e, ErrRunning):
					running++
				default:
					t.Fatalf("unexpected %v", e)
				}
			}
			for v := range res {
				if v != 0 {
					if first == 0 {
						first = v
					} else if v != first {
						t.Fatalf("run %d vs %d", v, first)
					}
				}
			}
			if conflict == 0 && (success != 1 || running != 99) {
				t.Fatalf("Fail success=%d running=%d", success, running)
			}
			if conflict == 1 && success != 100 {
				t.Fatalf("UseExisting success=%d", success)
			}
			if r.nextRun != 1 {
				t.Fatalf("nextRun=%d want 1", r.nextRun)
			}
		})
	}
}
