// Command demo exercises the snapshot differ; one OK/FAIL line per behavior.
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/merge"
	"ontology/snap"
)

var failed bool

func check(name string, ok bool) {
	fmt.Printf("%s  %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name)
	if !ok {
		failed = true
	}
}
func r(k int64, v string) snap.Row { return snap.Row{Key: k, Val: v} }
func s(rs ...snap.Row) []snap.Row  { return rs }
func mk(k byte, key int64, o, n string) merge.Change {
	return merge.Change{Kind: k, Key: key, Old: o, New: n}
}
func eq(a, b any) bool { return fmt.Sprint(a) == fmt.Sprint(b) }

func main() {
	// 1. Section-3 changelog + comparison count (count asserted inside the
	// package; the unexported counter never crosses an exported boundary).
	o := s(r(1, "a"), r(3, "b"), r(4, "c"), r(7, "d"), r(9, "e"))
	n := s(r(2, "x"), r(3, "b"), r(4, "C"), r(5, "y"), r(9, "e"), r(10, "z"), r(12, "w"))
	want := []merge.Change{mk('D', 1, "a", ""), mk('I', 2, "", "x"), mk('U', 4, "c", "C"),
		mk('I', 5, "", "y"), mk('D', 7, "d", ""), mk('I', 10, "", "z"), mk('I', 12, "", "w")}
	check("section-3 changelog and comparison count", eq(merge.Diff(o, n), want) && merge.SelfCheck() == nil)

	// 2. Tail remainder on both sides is emitted after exhaustion.
	check("tail remainder emitted (D1 D2 I4 I5)",
		eq(merge.Diff(s(r(1, "a"), r(2, "b")), s(r(4, "c"), r(5, "d"))),
			[]merge.Change{mk('D', 1, "a", ""), mk('D', 2, "b", ""), mk('I', 4, "", "c"), mk('I', 5, "", "d")}))

	// 3. Equal values produce no change.
	check("equal values not emitted (only U2)",
		eq(merge.Diff(s(r(1, "a"), r(2, "b")), s(r(1, "a"), r(2, "B"))),
			[]merge.Change{mk('U', 2, "b", "B")}))

	// 4 & 5. Replay equivalence and naive-reference agreement.
	rOK, nOK := true, true
	for _, p := range [][2][]snap.Row{
		{nil, s(r(1, "x"), r(2, "y"))},
		{s(r(1, "a"), r(3, "b")), s(r(2, "x"), r(3, "B"), r(4, "c"))},
		{s(r(9, "x")), nil},
	} {
		c := merge.Diff(p[0], p[1])
		if !eq(merge.Replay(p[0], c), p[1]) {
			rOK = false
		}
		if !eq(c, merge.Naive(p[0], p[1])) {
			nOK = false
		}
	}
	check("replay equivalence", rOK)
	check("matches naive reference", nOK)

	// 6. Four distinct decidable errors; the swapped input reports unsorted.
	_, e1 := api.New(nil, 0)
	_, e2 := api.New(s(r(1, "x"), r(1, "y")), 10)
	d3, _ := api.New(nil, 10)
	swapped := s(r(2, "x"), r(3, "b"), r(4, "C"), r(5, "y"), r(10, "z"), r(9, "e"), r(12, "w"))
	_, e3 := d3.Advance(swapped)
	d4, _ := api.New(nil, 2)
	_, e4 := d4.Advance(s(r(1, "v"), r(2, "v"), r(3, "v")))
	check("four decidable errors (config/dup/unsorted-swap/limit)",
		errors.Is(e1, api.ErrMaxChanges) && errors.Is(e2, api.ErrDuplicateKey) &&
			errors.Is(e3, api.ErrNotSorted) && errors.Is(e4, api.ErrTooManyChanges))

	// 7. Rejected advances leave snapshot/stats untouched; still usable.
	d5, _ := api.New(s(r(1, "a"), r(3, "b")), 2)
	before := d5.Current()
	_, _ = d5.Advance(swapped)
	_, _ = d5.Advance(s(r(7, "v"), r(8, "v"), r(9, "v")))
	a, tot := d5.Stats()
	unchanged := eq(d5.Current(), before) && a == 0 && tot == 0
	_, stillOK := d5.Advance(s(r(1, "a"), r(3, "B"))) // valid call must succeed
	check("state unchanged after rejection, still usable", unchanged && stillOK == nil)

	// 8. Disjoint old=1..n / new=n+1..n+m compares exactly n (internal).
	check("disjoint comparison count exactly n (internal assert)", merge.SelfCheck() == nil)

	// 9. Concurrent readers see only whole snapshots; indices non-decreasing.
	check("concurrent readers see whole snapshots only", concurrentOK())

	if failed {
		os.Exit(1)
	}
}

func concurrentOK() bool {
	const K, N = 200, 8
	ver := func(k int) (v []snap.Row) {
		for j := 0; j <= k; j++ {
			v = append(v, r(int64(j), strconv.Itoa(k)))
		}
		return
	}
	d, _ := api.New(ver(0), 1_000_000)
	var wg sync.WaitGroup
	var ok atomic.Bool
	ok.Store(true)
	spawn := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }
	for i := 0; i < N; i++ {
		spawn(func() { // every Current must equal one whole version
			for last := -1; ; {
				cur := d.Current()
				idx := len(cur) - 1
				if idx < last || !eq(cur, ver(idx)) {
					ok.Store(false)
					return
				}
				if idx == K { // only reachable after the writer drains K
					return
				}
				last = idx
			}
		})
	}
	spawn(func() { // the single writer; wg.Wait needs no sleep to be safe
		for k := 1; k <= K; k++ {
			if _, e := d.Advance(ver(k)); e != nil {
				ok.Store(false)
			}
		}
	})
	wg.Wait()
	return ok.Load()
}
