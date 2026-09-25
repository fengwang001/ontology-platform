package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Println(name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func logEq(got []any, want ...int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i].(int64) != want[i] {
			return false
		}
	}
	return true
}

func main() {
	a := api.New(16)
	check("selfcheck", a.SelfCheck() == nil)

	// Seven-step sequence from NOTES.md: per-step log and dup count.
	wantLog := [][]int64{{0}, {0}, {0, 1, 2}, {0, 1, 2}, {0, 1, 2, 3}, {0, 1, 2, 3}, {0, 1, 2, 3, 4}}
	wantDup := []int{0, 0, 0, 1, 1, 2, 2}
	ok := true
	for i, s := range []int64{0, 2, 1, 2, 3, 0, 4} {
		ok = ok && a.Deliver("S", s, s) == nil &&
			logEq(a.Delivered("S"), wantLog[i]...) && a.Dup("S") == wantDup[i]
	}
	check("seven-step judgments", ok)
	check("cascade delivery", logEq(a.Delivered("S"), 0, 1, 2, 3, 4))

	// Exactly-once: heavy duplicates, each seq delivered at most once.
	b := api.New(8)
	for _, s := range []int64{0, 0, 1, 1, 0, 2, 2, 1, 3, 3} {
		_ = b.Deliver("S", s, s)
	}
	check("exactly-once", logEq(b.Delivered("S"), 0, 1, 2, 3) && b.Dup("S") == 6)

	// FIFO: shuffled 0..999 delivered ascending, gap-free.
	c := api.New(1000)
	ok = true
	for _, p := range rand.New(rand.NewSource(1)).Perm(1000) {
		ok = ok && c.Deliver("S", int64(p), int64(p)) == nil
	}
	got := c.Delivered("S")
	for i, v := range got {
		ok = ok && v.(int64) == int64(i)
	}
	check("fifo order", ok && len(got) == 1000)

	// Naive reference: deduped arrivals sorted ascending.
	d := api.New(64)
	seen := map[int64]bool{}
	var want []int64
	for _, s := range []int64{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 0, 8, 7} {
		_ = d.Deliver("S", s, s)
		if !seen[s] {
			seen[s] = true
			want = append(want, s)
		}
	}
	for i := 1; i < len(want); i++ {
		for j := i; j > 0 && want[j] < want[j-1]; j-- {
			want[j], want[j-1] = want[j-1], want[j]
		}
	}
	check("naive reference", logEq(d.Delivered("S"), want...))

	// Three distinct decidable errors.
	e := api.New(1)
	_ = e.Deliver("F", 0, int64(0))
	_ = e.Deliver("F", 5, int64(5)) // buffer full now
	ok = errors.Is(e.Deliver("", 0, nil), api.ErrEmptySender) &&
		errors.Is(e.Deliver("F", -1, nil), api.ErrNegativeSeq) &&
		errors.Is(e.Deliver("F", 9, nil), api.ErrBufferFull)
	check("decidable errors", ok)
	ok = logEq(e.Delivered("F"), 0) && e.Dup("F") == 0 &&
		e.Deliver("F", 1, int64(1)) == nil // still usable
	check("rejection leaves no trace", ok)

	// Large m: a duplicate is still rejected correctly (O(1) by next;
	// the inspected-entry count is asserted in seq's internal test).
	f := api.New(4)
	for i := 0; i < 10000; i++ {
		_ = f.Deliver("S", int64(i), int64(i))
	}
	_ = f.Deliver("S", 0, nil)
	check("dedup at large m", f.Dup("S") == 1 && len(f.Delivered("S")) == 10000)

	// Concurrent: N goroutines, one sender, shuffled 0..N-1.
	const n = 500
	g := api.New(n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	ok = true
	var mu sync.Mutex
	for _, p := range rand.New(rand.NewSource(2)).Perm(n) {
		wg.Add(1)
		go func(s int64) {
			defer wg.Done()
			<-start
			if err := g.Deliver("S", s, s); err != nil {
				mu.Lock()
				ok = false
				mu.Unlock()
			}
		}(int64(p))
	}
	close(start)
	wg.Wait()
	got = g.Delivered("S")
	for i, v := range got {
		ok = ok && v.(int64) == int64(i)
	}
	check("concurrent delivery", ok && len(got) == n && g.Dup("S") == 0)

	if failed {
		os.Exit(1)
	}
}
