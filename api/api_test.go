package api

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func chk(t *testing.T, ok bool, f string, a ...any) {
	if !ok {
		t.Fatalf(f, a...)
	}
}

// ref is the naive single-step reference model shared by the tests.
type ref struct {
	c int
	q [][]int64
	d []int
}

func newRef(n, c int) *ref { return &ref{c: c, q: make([][]int64, n), d: make([]int, n)} }
func (r *ref) pub(v int64) { // enqueue if not full, else tail-drop newcomer
	for i := range r.q {
		if len(r.q[i]) < r.c {
			r.q[i] = append(r.q[i], v)
		} else {
			r.d[i]++
		}
	}
}
func (r *ref) con(i int) (int64, bool) {
	if len(r.q[i]) == 0 {
		return 0, false
	}
	v := r.q[i][0]
	r.q[i] = r.q[i][1:]
	return v, true
}
func (r *ref) match(a *API) bool {
	for i := range r.q {
		if a.QueueLen(i) != len(r.q[i]) || a.DropCount(i) != r.d[i] {
			return false
		}
	}
	return true
}

// TestNaiveReference pins invariant 1; case 0 is the NOTES six-step.
func TestNaiveReference(t *testing.T) {
	cases := []struct {
		n, c int
		ops  []op
	}{
		{2, 2, []op{{true, 1, 0}, {true, 2, 0}, {true, 3, 0}, {false, 0, 0}, {true, 4, 0}, {false, 0, 1}}},
		{2, 1, []op{{true, 1, 0}, {true, 2, 0}, {false, 0, 0}, {false, 0, 1}, {true, 3, 0}}},
		{3, 3, []op{{true, 1, 0}, {true, 2, 0}, {true, 3, 0}, {true, 4, 0}, {true, 5, 0}, {true, 6, 0}, {false, 0, 2}, {true, 7, 0}, {false, 0, 0}, {false, 0, 1}, {false, 0, 2}}},
	}
	for _, tc := range cases {
		a, _ := New(tc.n, tc.c)
		m := newRef(tc.n, tc.c)
		for i, o := range tc.ops {
			if o.pub {
				a.Publish(o.v)
				m.pub(o.v)
			} else {
				ev, ok := a.Consume(o.si)
				rv, rok := m.con(o.si)
				chk(t, ev == rv && ok == rok, "n=%d c=%d op %d consume", tc.n, tc.c, i)
			}
			chk(t, m.match(a), "n=%d c=%d op %d state", tc.n, tc.c, i)
		}
		chk(t, fmt.Sprint(drainAll(a, tc.n)) == fmt.Sprint(m.q), "n=%d c=%d final", tc.n, tc.c)
	}
	sc, _ := New(2, 2)
	chk(t, sc.SelfCheck() == nil, "SelfCheck failed")
}

// TestSlowConsumerIsolation pins invariant 2.
func TestSlowConsumerIsolation(t *testing.T) {
	a, _ := New(3, 2)
	for _, v := range []int64{1, 2, 3} {
		a.Publish(v)
	}
	a.Consume(0)
	a.Consume(0) // only s0 drained
	a.Publish(4) // returns at once; s1/s2 full, s0 has room
	h, ok := a.Consume(0)
	chk(t, ok && h == 4, "fast s0 (%v,%v) want 4", h, ok)
	for si := 1; si < 3; si++ {
		chk(t, a.DropCount(si) == 2, "s%d drops %d want 2", si, a.DropCount(si))
		h, _ := a.Consume(si)
		chk(t, h == 1, "s%d oldest %d want 1 (starved?)", si, h)
	}
}

// TestRejectedOpsLeaveNoTrace pins invariant 4.
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	_, eN := New(0, 2)
	_, eC := New(2, 0)
	chk(t, errors.Is(eN, ErrInvalidN), "N<=0 %v", eN)
	chk(t, errors.Is(eC, ErrInvalidC), "C<=0 %v", eC)
	chk(t, ErrInvalidN != ErrInvalidC && ErrInvalidN != ErrInvalidSubscriber && ErrInvalidC != ErrInvalidSubscriber, "sentinels not distinct")
	a, _ := New(2, 2)
	for _, v := range []int64{5, 6, 7} {
		a.Publish(v)
	}
	l0, d0 := a.QueueLen(0), a.DropCount(0)
	bad := []func(int){func(s int) { a.Consume(s) }, func(s int) { _ = a.DropCount(s) }, func(s int) { _ = a.QueueLen(s) }}
	for _, si := range []int{-1, 2} {
		for _, f := range bad {
			r := guard(func() { f(si) })
			chk(t, errors.Is(r.(error), ErrInvalidSubscriber), "si=%d recovered %v", si, r)
		}
	}
	chk(t, a.QueueLen(0) == l0 && a.DropCount(0) == d0, "state changed after rejected calls")
	h, ok := a.Consume(0)
	chk(t, ok && h == 5, "oldest (%v,%v) want 5", h, ok)
	a.Publish(8) // still usable
}

func TestConcurrentConsume(t *testing.T) {
	const n, c = 8, 16
	a, _ := New(n, c)
	for v := int64(1); v <= c; v++ {
		a.Publish(v)
	}
	got := make([][]int64, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for si := 0; si < n; si++ {
		wg.Add(1)
		go func(si int) {
			defer wg.Done()
			<-start
			for ev, ok := a.Consume(si); ok; ev, ok = a.Consume(si) {
				got[si] = append(got[si], ev)
			}
		}(si)
	}
	close(start)
	wg.Wait()
	for si := 0; si < n; si++ {
		chk(t, a.QueueLen(si) == 0 && len(got[si]) == c, "s%d drained %v leftover %d", si, got[si], a.QueueLen(si))
		for j := 0; j < c; j++ {
			chk(t, got[si][j] == int64(j+1), "s%d order %v want 1..%d", si, got[si], c)
		}
	}
}
