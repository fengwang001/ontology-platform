package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/fifo"
)

var fails int

func check(name string, ok bool) {
	s := "OK "
	if !ok {
		fails++
		s = "FAIL "
	}
	fmt.Println(s + name)
}
func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func seq1to(n int64) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(i) + 1
	}
	return out
}

// naiveRef re-implements the spec rules independently with a map.
func naiveRef(max int, seqs []int64) (emitted []int64, dropped int64) {
	next := int64(1)
	held := map[int64]bool{}
	for _, s := range seqs {
		switch {
		case s < next:
			dropped++
		case s == next:
			for { // emit next, then drain the buffered consecutive prefix
				emitted, next = append(emitted, next), next+1
				if !held[next] {
					break
				}
				delete(held, next)
			}
		case !held[s] && len(held) < max:
			held[s] = true
		}
	}
	return
}

func main() {
	b, _ := api.New(3) // 1) eight-step trace incl. the step-4 cascade
	seqs := []int64{5, 2, 4, 1, 3, 6, 7, 2}
	wantBuf := []int{1, 2, 3, 2, 0, 0, 0, 0}
	wantOut := [][]int64{nil, nil, nil, {1, 2}, {3, 4, 5}, {6}, {7}, nil}
	ok8 := true
	for i, s := range seqs {
		out, err := b.Feed("K", s)
		if err != nil || b.Buffered("K") != wantBuf[i] || !eq(out, wantOut[i]) {
			ok8 = false
		}
	}
	check("eight-step bufs/emits; step4 cascades [1,2]",
		ok8 && b.Dropped() == 1 && eq(b.Emitted("K"), seq1to(7)))
	probeOK := true // 2) probe count O(m), not O(m^2); SelfCheck clean
	for _, m := range []int{100, 1000, 10000} {
		g, _ := api.New(m + 1)
		for s := int64(m + 1); s >= 2; s-- {
			if _, e := g.Feed("K", s); e != nil {
				probeOK = false
			}
		}
		if _, e := g.Feed("K", 1); e != nil || g.Buffered("K") != 0 ||
			!eq(g.Emitted("K"), seq1to(int64(m)+1)) {
			probeOK = false
		}
	}
	check("probe count O(m) at m=100..10000; SelfCheck clean", probeOK && fifo.SelfCheck() == nil)
	d, _ := api.New(3) // 3) strict prefix == sorted accepted (naive reference)
	plans := map[string][]int64{"K": seqs, "J": {4, 3, 2, 1, 5, 1}, "M": {2, 3, 4, 1, 5}}
	refOK, drops := true, int64(0)
	for key, ss := range plans {
		want, dr := naiveRef(3, ss)
		drops += dr
		for _, s := range ss {
			_, _ = d.Feed(key, s)
		}
		if !eq(d.Emitted(key), want) || !eq(d.Emitted(key), seq1to(int64(len(d.Emitted(key))))) {
			refOK = false
		}
	}
	check("out-of-order strict prefix == sorted accepted (naive reference)", refOK && d.Dropped() == drops)
	errOK := true // 4) three distinct errors; rejects leave no trace; still usable
	_, bad := api.New(0)
	errOK = errOK && errors.Is(bad, api.ErrInvalidMax)
	for _, s := range []int64{9, 8, 7} {
		_, _ = b.Feed("Q", s)
	}
	snap := b.Buffered("Q")
	_, e1 := b.Feed("Q", 10)
	_, e2 := b.Feed("", 1)
	errOK = errOK && errors.Is(e1, api.ErrBackpressure) && errors.Is(e2, api.ErrEmptyKey)
	errOK = errOK && b.Buffered("Q") == snap && b.Dropped() == 1
	three := api.ErrEmptyKey != api.ErrInvalidMax && api.ErrEmptyKey != api.ErrBackpressure &&
		api.ErrInvalidMax != api.ErrBackpressure
	_, useErr := b.Feed("Q", 1)
	check("three distinct errors; rejects no-trace; still usable; SelfCheck",
		errOK && three && useErr == nil &&
			eq(b.Emitted("Q"), seq1to(int64(len(b.Emitted("Q"))))) && b.SelfCheck() == nil)
	const N, L = 12, 300 // 5) N goroutines, distinct keys, reversed 1..L
	c, _ := api.New(L + 1)
	var wg sync.WaitGroup
	for n := 0; n < N; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for s := int64(L); s >= 1; s-- {
				if _, e := c.Feed(string(rune('a'+n)), s); e != nil {
					panic(e)
				}
			}
		}(n)
	}
	wg.Wait()
	concOK := true
	for n := 0; n < N; n++ {
		if e := c.Emitted(string(rune('a' + n))); len(e) != L || !eq(e, seq1to(L)) {
			concOK = false
		}
	}
	check("concurrent distinct-key feeds: every key emits exactly 1..L ordered", concOK)
	if fails > 0 {
		fmt.Println("DEMO FAILED")
		os.Exit(1)
	}
}
