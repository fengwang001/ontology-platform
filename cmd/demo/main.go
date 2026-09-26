// Command demo exercises the token-bucket rate limiter and prints OK/FAIL
// lines (at most ten). It takes no arguments and performs no network I/O.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/lim"
)

var failures int

func ok(name string, pass bool, detail string) {
	tag := "OK  "
	if !pass {
		tag, failures = "FAIL ", failures+1
	}
	fmt.Printf("%s%s %s\n", tag, name, detail)
}

func main() {
	// The eight worked steps: each entry is decision + post-decision tokens.
	l, _ := api.New(20, 2)
	reqs := [][2]int64{{0, 15}, {0, 8}, {3, 10}, {3, 5}, {8, 12}, {9, 6}, {20, 18}, {20, 3}}
	want := []bool{true, false, true, false, false, true, true, false}
	wantTok := []int64{5, 5, 1, 1, 11, 7, 2, 2}
	detail := ""
	pass := true
	for i, r := range reqs {
		a, err := l.Allow(r[0], r[1])
		pass = pass && err == nil && a == want[i] && l.Tokens() == wantTok[i]
		d := "R"
		if a {
			d = "A"
		}
		detail += fmt.Sprintf("%d:%s→%d ", i+1, d, l.Tokens())
	}
	ok("eight steps (verdict→tokens)", pass, detail)

	// Refill cap: empty bucket, long elapsed time must still stop at capacity.
	c, _ := api.New(5, 1)
	c.Allow(0, 5)
	c.Allow(100, 1)
	ok("refill capped at capacity", c.Tokens() == 4, fmt.Sprintf("tokens=%d (uncapped would be 94)", c.Tokens()))

	// Naive-reference equivalence plus token bounds over a generated sequence.
	g, _ := api.New(17, 3)
	var ref, prevT, now, seed int64 = 17, 0, 0, 1
	bounded, equiv := true, true
	for i := 0; i < 1000; i++ {
		seed = seed*1103515245 + 12345
		delta := (seed >> 16) % 4
		if delta < 0 {
			delta += 4
		}
		now += delta + 1
		need := (seed>>8)%19 + 1
		if need < 1 {
			need += 19
		}
		ref = min(17, ref+(now-prevT)*3)
		prevT = now
		a, _ := g.Allow(now, need)
		if a {
			ref -= need
		}
		tok := g.Tokens()
		bounded = bounded && tok >= 0 && tok <= 17
		equiv = equiv && tok == ref
	}
	ok("matches naive reference", equiv, "")
	ok("tokens stay within [0,capacity]", bounded, "")

	// The three distinct sentinel errors.
	bad1, e1 := api.New(0, 1)
	s, _ := api.New(20, 2)
	_, e2 := s.Allow(0, 0) // invalid need
	s.Allow(1, 1)          // valid, advances last to 1
	_, e3 := s.Allow(0, 1) // clock rollback (t=0 < last=1)
	distinct := errors.Is(e1, api.ErrInvalidConfig) && errors.Is(e2, lim.ErrInvalidNeed) &&
		errors.Is(e3, lim.ErrClockRollback) && !errors.Is(e2, lim.ErrClockRollback)
	ok("three distinct sentinel errors", bad1 == nil && distinct,
		fmt.Sprintf("{%v | %v | %v}", e1, e2, e3))

	// A rejected (insufficient tokens) request leaves tokens unchanged.
	r, _ := api.New(5, 1)
	a, _ := r.Allow(0, 6)
	ok("rejection leaves state untouched", !a && r.Tokens() == 5, fmt.Sprintf("tokens=%d", r.Tokens()))

	// Large refill totals m: the O(1) flag (only a boolean; the internal
	// counter value is never exported) must hold independent of m.
	o1 := true
	ms := ""
	for _, m := range []int64{100, 1000, 5000, 10000} {
		b := lim.New(m*2, 1)
		b.Allow(0, m*2) // empty
		b.Allow(m, 1)   // elapsed*rate == m, tiny need
		o1 = o1 && b.RefillIsO1()
		ms += fmt.Sprintf("%d ", m)
	}
	ok("refill is O(1) for m =", o1, ms+"(internal counter unexported, stays 0)")

	// Concurrency: N goroutines at the same timestamp, no sleeps.
	const N = 64
	p, _ := api.New(N, 2)
	var admitted int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if a, err := p.Allow(10, 1); err == nil && a {
				atomic.AddInt64(&admitted, 1)
			}
		}()
	}
	close(start)
	wg.Wait()
	ok("concurrent same-timestamp admits", admitted == N && p.Tokens() == 0,
		fmt.Sprintf("admitted=%d/%d tokens=%d", admitted, N, p.Tokens()))

	ok("SelfCheck (4 invariants)", l.SelfCheck() == nil, "")
	if failures > 0 {
		fmt.Printf("DEMO FAILED: %d check(s)\n", failures)
		os.Exit(1)
	}
}
