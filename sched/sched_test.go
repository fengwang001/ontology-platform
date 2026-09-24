package sched

import (
	"slices"
	"testing"
)

// naive is an independent simulation of the scheduling rules (reference oracle).
type naive struct {
	qs   map[int][]int
	cur  *Frame
	stk  []Frame
	done []int
}

func newNaive() *naive { return &naive{qs: map[int][]int{}} }

func (n *naive) submit(id, prio int) {
	n.qs[prio] = append(n.qs[prio], id)
	if n.cur != nil && prio > n.cur.Prio {
		n.stk = append(n.stk, *n.cur)
		n.cur = &Frame{Prio: prio}
	}
}

func (n *naive) process() bool {
	if n.cur == nil {
		best, ok := -1, false
		for p, q := range n.qs {
			if len(q) > 0 && (!ok || p > best) {
				best, ok = p, true
			}
		}
		if !ok {
			return false
		}
		n.cur = &Frame{Prio: best}
	}
	n.done = append(n.done, n.qs[n.cur.Prio][n.cur.Pos])
	n.cur.Pos++
	if n.cur.Pos >= len(n.qs[n.cur.Prio]) {
		delete(n.qs, n.cur.Prio)
		if m := len(n.stk); m > 0 {
			n.cur = &n.stk[m-1]
			n.stk = n.stk[:m-1]
		} else {
			n.cur = nil
		}
	}
	return true
}

// run plays ops ({id,prio}=submit, {0,-1}=process) on both implementations,
func run(t *testing.T, ops [][2]int) (done, submitted []int) {
	t.Helper()
	s := New()
	n := newNaive()
	for _, op := range ops {
		if op[1] >= 0 {
			if err := s.Submit(op[0], op[1]); err != nil {
				t.Fatalf("submit(%d,%d): %v", op[0], op[1], err)
			}
			n.submit(op[0], op[1])
			submitted = append(submitted, op[0])
		} else {
			_, _, err := s.Process()
			if ok := n.process(); !ok {
				if err != ErrIdle {
					t.Fatalf("process: got %v, want ErrIdle", err)
				}
			} else if err != nil {
				t.Fatalf("process: %v", err)
			}
		}
		if got := s.Done(); !slices.Equal(got, n.done) {
			t.Fatalf("done=%v, naive wants %v", got, n.done)
		}
	}
	for len(n.done) < len(submitted) {
		if _, _, err := s.Process(); err != nil {
			t.Fatalf("drain: %v", err)
		}
		n.process()
	}
	if got := s.Done(); !slices.Equal(got, n.done) {
		t.Fatalf("drained done=%v, naive wants %v", got, n.done)
	}
	if st := s.Snapshot(); len(st.Pending) != 0 || st.Current != nil || len(st.Suspend) != 0 {
		t.Fatal("not fully drained: pending/current/suspend must all be empty")
	}
	return s.Done(), submitted
}

func TestNaiveConsistency(t *testing.T) {
	script := [][2]int{{1, 1}, {2, 1}, {0, -1}, {3, 3}, {4, 5}, {0, -1}, {0, -1}, {0, -1}}
	if done, _ := run(t, script); !slices.Equal(done, []int{1, 4, 3, 2}) {
		t.Fatalf("script done=%v, want [1 4 3 2]", done)
	}
	rng := uint32(7)
	next := func(n uint32) uint32 { rng = rng*1664525 + 1013904223; return rng % n }
	for _, cs := range []struct{ ops, maxP int }{{200, 4}, {1000, 16}, {2000, 3}} {
		var ops [][2]int
		id := 0
		for i := 0; i < cs.ops; i++ {
			if next(100) < 55 {
				ops = append(ops, [2]int{id, int(next(uint32(cs.maxP)))})
				id++
			} else {
				ops = append(ops, [2]int{0, -1})
			}
		}
		done, submitted := run(t, ops)
		a, b := slices.Clone(done), slices.Clone(submitted)
		slices.Sort(a)
		slices.Sort(b)
		if !slices.Equal(a, b) {
			t.Fatalf("multiset mismatch: %d done vs %d submitted", len(a), len(b))
		}
		last, prioOf := map[int]int{}, map[int]int{}
		for _, op := range ops {
			if op[1] >= 0 {
				prioOf[op[0]] = op[1]
			}
		}
		for _, id := range done { // ids grow with arrival: same-prio must be increasing
			if id < last[prioOf[id]] {
				t.Fatalf("FIFO violated at prio %d", prioOf[id])
			}
			last[prioOf[id]] = id
		}
	}
}

func TestPickChecksBucketsNotEvents(t *testing.T) {
	const P = 16
	for _, n := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < n; i++ {
			if err := s.Submit(i, i%P); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := s.Process(); err != nil {
			t.Fatal(err)
		}
		if s.lastChecked > P {
			t.Fatalf("n=%d: checked %d buckets, want <= P=%d", n, s.lastChecked, P)
		}
	}
}
