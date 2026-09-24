package hagg

import (
	"reflect"
	"strconv"
	"sync"
	"testing"

	"ontology/hop"
)

// TestRejectionLeavesNoTrace pins invariant 4: bad params, clock
// rollback, over-limit Adds and empty keys fail with pairwise distinct
// sentinels, change nothing, and the instance stays usable afterwards.
func TestRejectionLeavesNoTrace(t *testing.T) {
	cases := []struct{ size, slide int64 }{ // table of every illegal shape
		{0, 4}, {-1, 4}, {4, 0}, {4, -4}, {12, 5}, {5, 4},
	}
	for _, q := range cases {
		if _, err := New(q.size, q.slide, 10); err != ErrBadParams {
			t.Fatalf("params %+v want ErrBadParams, got %v", q, err)
		}
	}
	a, _ := New(12, 4, 3)
	if err := a.Add("a", 0); err != nil {
		t.Fatal(err)
	}
	snapOpen, snapOut := len(a.open), len(a.Results())
	if err := a.Add("b", 0); err != ErrTooManyOpen {
		t.Fatalf("want ErrTooManyOpen, got %v", err)
	}
	if err := a.Add("", 0); err != ErrEmptyKey {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	if len(a.open) != snapOpen || len(a.Results()) != snapOut || a.Dropped() != 0 {
		t.Fatal("rejected Add changed open windows, outputs or drop count")
	}
	if _, err := a.Advance(4); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Advance(3); err != ErrClockBack {
		t.Fatalf("want ErrClockBack, got %v", err)
	}
	rs := a.Flush() // rejected "b" never counted: a's k=-1 and k=0 remain
	if len(rs) != 2 || rs[0].Key != "a" || rs[1].Key != "a" || a.Dropped() != 0 {
		t.Fatalf("state after rejections wrong: %v drop=%d", rs, a.Dropped())
	}
	if ErrBadParams == ErrClockBack || ErrClockBack == ErrTooManyOpen ||
		ErrTooManyOpen == ErrEmptyKey || ErrBadParams == ErrEmptyKey {
		t.Fatal("the four sentinel errors must be pairwise distinct")
	}
}

// TestScanBound pins section four: a close-nothing Advance inspects a
// constant number of heap entries regardless of open-window count m,
// and one closing exactly c windows inspects at most c plus a constant.
// The counter is read white-box here; it is never reachable via any
// exported method.
func TestScanBound(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} { // table over open-window scales
		a, _ := New(4, 4, m*2+8) // size==slide: each event opens one window
		for i := 0; i < m; i++ {
			if err := a.Add("k"+strconv.Itoa(i), 1<<40); err != nil { // far-future ends
				t.Fatal(err)
			}
		}
		if rs, _ := a.Advance(0); len(rs) != 0 || a.last > 1 {
			t.Fatalf("m=%d close-none inspected %d entries", m, a.last)
		}
		c := m / 7
		for i := 0; i < c; i++ { // c extra windows with end=4
			if err := a.Add("c"+strconv.Itoa(i), 0); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := a.Advance(0); err != nil || a.last > 1 { // equal time, closes none
			t.Fatalf("m=%d second close-none inspected %d", m, a.last)
		}
		rs, err := a.Advance(4) // closes exactly the c windows, then one peek past them
		if err != nil || len(rs) != c || a.last > int64(c)+1 {
			t.Fatalf("m=%d c=%d closed=%d inspected=%d err=%v", m, c, len(rs), a.last, err)
		}
	}
}

// TestConcurrent: N goroutines Add disjoint keys concurrently and the
// flushed result must match the naive reference; then N readers of the
// flushed instance must receive field-identical snapshots. No sleeps.
func TestConcurrent(t *testing.T) {
	const N, per = 16, 50
	a, _ := New(12, 4, N*per*3+100)
	var wg sync.WaitGroup
	var failMu sync.Mutex
	failed := false
	fail := func(format string, args ...any) {
		failMu.Lock()
		failed = true
		t.Errorf(format, args...)
		failMu.Unlock()
	}
	collected := make(chan nEv, N*per)
	for g := 0; g < N; g++ { // each goroutine owns a distinct key
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := "g" + strconv.Itoa(g)
			for i := 0; i < per; i++ {
				ts := int64((g*7+i*13)%200 - 60)
				if err := a.Add(key, ts); err != nil {
					fail("concurrent Add: %v", err)
					return
				}
				collected <- nEv{key, ts, -1 << 63} // clock is still -inf for every writer
			}
		}(g)
	}
	wg.Wait()
	close(collected)
	if failed {
		t.Fatal("concurrent Adds failed")
	}
	evs := make([]nEv, 0, N*per)
	for e := range collected {
		evs = append(evs, e)
	}
	a.Flush()
	want, drop := naive(hop.Params{Size: 12, Slide: 4}, evs)
	if !reflect.DeepEqual(toMap(a.Results()), want) || a.Dropped() != drop {
		t.Fatal("concurrent-writer result differs from naive reference")
	}
	snaps, drops := make([][]Result, N), make([]int64, N)
	for i := 0; i < N; i++ { // concurrent read-only access after Flush
		wg.Add(1)
		go func(i int) { defer wg.Done(); snaps[i], drops[i] = a.Results(), a.Dropped() }(i)
	}
	wg.Wait()
	for i := 1; i < N; i++ {
		if !reflect.DeepEqual(snaps[i], snaps[0]) || drops[i] != drops[0] {
			t.Fatalf("reader %d snapshot differs field-by-field", i)
		}
	}
}
