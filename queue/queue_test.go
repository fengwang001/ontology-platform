package queue

import "testing"

func fill(t *testing.T, q *Queue, prio, n int, size int64) {
	t.Helper()
	for i := 0; i < n; i++ {
		q.Enqueue("t", prio, size)
	}
}

func checkInvariant(t *testing.T, q *Queue) {
	t.Helper()
	used, per := q.queuedBytes()
	if used != q.Used() {
		t.Fatalf("used=%d want %d", q.Used(), used)
	}
	for p := 0; p < NumPrio; p++ {
		if per[p] != q.prioBytes[p] {
			t.Fatalf("prioBytes[%d]=%d want %d", p, q.prioBytes[p], per[p])
		}
	}
	if q.Used() > q.Cap() {
		t.Fatalf("used %d exceeds cap %d", q.Used(), q.Cap())
	}
}

func TestEvictOrderShortestPrefix(t *testing.T) {
	q, _ := New(1000)
	fill(t, q, 1, 2, 50)  // seq 1,2 prio 1
	fill(t, q, 2, 3, 100) // seq 3,4,5 prio 2
	ev := q.Evict(0, 150)
	// Lowest priority first, newest first, stop once need is met:
	// seq 5 (100) then seq 4 (100) frees 200 >= 150; seq 3 stays.
	if len(ev) != 2 || ev[0].Seq != 5 || ev[1].Seq != 4 {
		t.Fatalf("evicted=%v want seq [5 4]", ev)
	}
	if n, _ := q.examined(); n > len(ev)+1 {
		t.Fatalf("examined %d > evicted+1 %d", n, len(ev)+1)
	}
	if q.Used() != 200 {
		t.Fatalf("used=%d want 200", q.Used())
	}
	checkInvariant(t, q)
}

func TestEvictSkipsSameAndHigherPrio(t *testing.T) {
	q, _ := New(100)
	fill(t, q, 0, 1, 10)
	fill(t, q, 1, 1, 10)
	fill(t, q, 2, 1, 10)
	if got := q.EvictableBytes(2); got != 0 {
		t.Fatalf("EvictableBytes(2)=%d want 0", got)
	}
	if got := q.EvictableBytes(1); got != 10 {
		t.Fatalf("EvictableBytes(1)=%d want 10", got)
	}
	ev := q.Evict(1, 5)
	if len(ev) != 1 || ev[0].Prio != 2 {
		t.Fatalf("evicted=%v want the single prio-2 record", ev)
	}
	checkInvariant(t, q)
}

func TestEvictExaminedBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		q, _ := New(int64(n))
		fill(t, q, 2, n, 1)
		need := int64(n / 2)
		ev := q.Evict(0, need)
		ex, _ := q.examined()
		if int64(len(ev)) != need || ex > len(ev)+1 {
			t.Fatalf("n=%d evicted=%d examined=%d", n, len(ev), ex)
		}
		for i, r := range ev { // newest first
			if r.Seq != uint64(n-i) {
				t.Fatalf("n=%d ev[%d].Seq=%d want %d", n, i, r.Seq, n-i)
			}
		}
		checkInvariant(t, q)
	}
}

func TestDrainStopsAtFirstTooBig(t *testing.T) {
	q, _ := New(100)
	q.Enqueue("t", 0, 30)
	q.Enqueue("t", 1, 10) // smaller but lower priority: must not be reached
	q.Enqueue("t", 0, 5)
	out := q.Drain(20)
	if len(out) != 0 {
		t.Fatalf("drained=%v want none (first record too big)", out)
	}
	if _, ex := q.examined(); ex != 1 {
		t.Fatalf("examined=%d want 1", ex)
	}
	out = q.Drain(35)
	if len(out) != 2 || out[0].Size != 30 || out[1].Size != 5 {
		t.Fatalf("drained=%v want [30 5]", out)
	}
	checkInvariant(t, q)
}

func TestDrainExaminedBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		q, _ := New(int64(n))
		fill(t, q, 0, n, 1)
		out := q.Drain(int64(n) - 1)
		if _, ex := q.examined(); ex != len(out)+1 {
			t.Fatalf("n=%d drained=%d examined=%d want drained+1", n, len(out), ex)
		}
		out = q.Drain(1)
		if _, ex := q.examined(); ex != len(out) {
			t.Fatalf("n=%d drained=%d examined=%d want drained", n, len(out), ex)
		}
		checkInvariant(t, q)
	}
}
