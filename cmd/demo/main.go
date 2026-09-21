// Command demo exercises the reassembler's nine core semantics end to end
// and prints one OK/FAIL verdict per scenario. It always exits 0.
package main

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ontology/budget"
	"ontology/frag"
	"ontology/reasm"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

var passed int

func check(name string, ok bool) {
	verdict := "OK  "
	if !ok {
		verdict = "FAIL"
	} else {
		passed++
	}
	fmt.Printf("%s %s\n", verdict, name)
}

const total = 9

func main() {
	clk := &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	// 1. Out-of-order assembly, delivered exactly once, byte-exact.
	r := reasm.New(1<<20, time.Minute, clk.Now)
	msg := []byte("hello ontology")
	deliveries, got := 0, []byte(nil)
	for _, p := range [][2]int{{7, 14}, {0, 5}, {5, 7}} {
		b, done, err := r.Submit("m1", p[0], msg[p[0]:p[1]], len(msg))
		if err == nil && done {
			deliveries++
			got = b
		}
	}
	check("out-of-order assembly delivered once, byte-exact",
		deliveries == 1 && string(got) == string(msg) && r.Used() == 0)

	// 2. Duplicate fragment is idempotent and not double-charged.
	r2 := reasm.New(1<<20, time.Minute, clk.Now)
	_, _, e1 := r2.Submit("m", 0, []byte("abc"), 6)
	used := r2.Used()
	_, done, e2 := r2.Submit("m", 0, []byte("abc"), 6)
	check("duplicate fragment idempotent, not re-charged",
		e1 == nil && e2 == nil && !done && r2.Used() == used && r2.Status("m").Received == 3)

	// 3. Overlapping conflict is detectable with offsets, state unpolluted.
	r3 := reasm.New(1<<20, time.Minute, clk.Now)
	_, _, _ = r3.Submit("m", 0, []byte("01234"), 10)
	_, _, err := r3.Submit("m", 3, []byte("XXab"), 10)
	var ce *frag.ConflictError
	conflictOK := errors.As(err, &ce) && ce.Start == 3 && ce.End == 5
	_, _, err = r3.Submit("m", 0, []byte("01234"), 10)
	check("conflict detectable with span, state unpolluted",
		conflictOK && err == nil && r3.Status("m").Received == 5)

	// 4. Adjacent and overlapping intervals merge into one.
	set, _ := frag.NewSet(15)
	_, _ = set.Add(0, []byte("0123456789"), nil)
	_, _ = set.Add(5, []byte("56789abcde"), nil)
	ivs := set.Intervals()
	adj, _ := frag.NewSet(10)
	_, _ = adj.Add(0, []byte("01234"), nil)
	_, _ = adj.Add(5, []byte("56789"), nil)
	aivs := adj.Intervals()
	check("overlap and adjacency merge into one interval",
		len(ivs) == 1 && ivs[0] == (frag.Interval{Start: 0, End: 15}) &&
			len(aivs) == 1 && aivs[0] == (frag.Interval{Start: 0, End: 10}))

	// 5. Out-of-bounds and invalid input rejected with distinct errors.
	_, _, e5a := r3.Submit("m", 8, []byte("abc"), 10)
	_, _, e5b := r3.Submit("m", 0, nil, 10)
	_, _, e5c := r3.Submit("m", 0, []byte("a"), 99)
	_, _, e5d := reasm.New(1<<20, time.Minute, clk.Now).Submit("z", 0, []byte("a"), 0)
	check("out-of-bounds/empty/zero-total/total-mismatch errors distinct",
		errors.Is(e5a, frag.ErrOutOfBounds) && errors.Is(e5b, frag.ErrEmptyData) &&
			errors.Is(e5c, reasm.ErrTotalMismatch) && errors.Is(e5d, frag.ErrZeroTotal) &&
			!errors.Is(e5a, e5b) && !errors.Is(e5c, e5d))

	// 6. Expiry at the exact deadline evicts and releases the budget.
	r6 := reasm.New(1<<20, 10*time.Second, clk.Now)
	_, _, _ = r6.Submit("m", 0, []byte("ab"), 4)
	clk.Advance(10 * time.Second)
	st6 := r6.Status("m")
	_, done6, e6 := r6.Submit("m", 0, []byte("xy"), 4)
	check("expiry at deadline evicts, budget released, ID restarts",
		st6 == (reasm.Status{}) && e6 == nil && !done6 &&
			r6.Used() == 2 && r6.Status("m").Remaining == 10*time.Second)

	// 7. Over-budget fragment rejected with zero state change.
	r7 := reasm.New(4, time.Minute, clk.Now)
	_, _, _ = r7.Submit("m", 0, []byte("ab"), 4)
	_, _, e7 := r7.Submit("n", 0, []byte("cde"), 5)
	check("over-budget fragment rejected, state unchanged",
		errors.Is(e7, budget.ErrExhausted) && r7.Used() == 2 &&
			r7.Status("m").Received == 2 && r7.Status("n") == (reasm.Status{}))

	// 8. Querying a delivered message yields zero values.
	st8 := r.Status("m1")
	check("delivered message queries as zero value", st8 == (reasm.Status{}))

	// 9. Concurrent submitters: exactly one caller observes delivery.
	r9 := reasm.New(1<<20, time.Minute, clk.Now)
	payload := []byte("concurrent delivery!")
	var wins atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < len(payload); i += 8 {
				_, done, err := r9.Submit("m", i, payload[i:i+1], len(payload))
				if err == nil && done {
					wins.Add(1)
				}
			}
		}(w)
	}
	wg.Wait()
	check("concurrent submitters: exactly one delivery", wins.Load() == 1 && r9.Used() == 0)

	fmt.Printf("TOTAL %d/%d checks passed\n", passed, total)
}
