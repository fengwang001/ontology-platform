// Command demo exercises the state-machine-replication packages and prints
// OK/FAIL lines (no args, no network). Exit code is non-zero on any failure.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/sm"
)

var fail bool

func report(ok bool, format string, args ...any) {
	tag := "OK"
	if !ok {
		tag, fail = "FAIL", true
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}

func main() {
	report(api.New().SelfCheck() == nil, "SelfCheck four invariants + incremental read")

	// Five-step scenario; each tuple is (committed, lastApplied, State).
	a := api.New()
	for _, c := range []sm.Cmd{sm.Add(2), sm.Mul(3), sm.Add(1), sm.Add(5)} {
		_ = a.Append(c)
	}
	rows := [][3]int{{0, 0, 0}}
	_ = a.Commit(3)
	rows = append(rows, [3]int{a.Committed(), a.LastApplied(), a.State()})
	a.Apply()
	rows = append(rows, [3]int{a.Committed(), a.LastApplied(), a.State()})
	_ = a.Restart(2, 6)
	_ = a.Commit(4)
	rows = append(rows, [3]int{a.Committed(), a.LastApplied(), a.State()})
	a.Apply()
	rows = append(rows, [3]int{a.Committed(), a.LastApplied(), a.State()})
	report(equal(rows, [][3]int{{0, 0, 0}, {3, 0, 0}, {3, 3, 7}, {4, 2, 6}, {4, 4, 12}}),
		"five-step (c,la,st) S1..S5 = %v", rows)

	// Batching: one big Apply vs per-index commits/applies converge.
	mk := func() *api.API {
		b := api.New()
		for k := 0; k < 40; k++ {
			if k%2 == 0 {
				_ = b.Append(sm.Add(k + 1))
			} else {
				_ = b.Append(sm.Mul(2))
			}
		}
		return b
	}
	one, many := mk(), mk()
	_ = one.Commit(40)
	one.Apply()
	for i := 1; i <= 40; i++ {
		_ = many.Commit(i)
		many.Apply()
	}
	report(one.State() == many.State(), "batched=%d vs incremental=%d converge", one.State(), many.State())

	// Three distinct, decidable sentinel errors.
	r := api.New()
	_ = r.Append(sm.Add(5))
	_ = r.Commit(1)
	r.Apply()
	cases := []struct {
		err error
		op  func() error
	}{{api.ErrEmptyCommand, func() error { return r.Append(sm.Cmd{}) }},
		{api.ErrCommitOutOfRange, func() error { return r.Commit(9) }},
		{api.ErrSnapshotOutOfRange, func() error { return r.Restart(9, 0) }}}
	distinct := errors.Is(cases[0].op(), cases[0].err) && errors.Is(cases[1].op(), cases[1].err) &&
		errors.Is(cases[2].op(), cases[2].err) && cases[0].err != cases[1].err &&
		cases[1].err != cases[2].err && cases[0].err != cases[2].err
	report(distinct, "three distinct decidable sentinel errors")

	report(r.Committed() == 1 && r.LastApplied() == 1 && r.State() == 5,
		"rejected ops left no trace (c=%d la=%d st=%d)", r.Committed(), r.LastApplied(), r.State())

	okm := true
	for _, m := range []int{100, 1000, 10000} {
		b := mkLog(m)
		_ = b.Commit(m - 1)
		b.Apply()
		_ = b.Commit(m)
		b.Apply()
		okm = okm && b.LastApplyReadOne()
	}
	report(okm, "final Apply reads exactly 1 entry for m=100,1000,10000 (O(1) resume)")

	ch := make(chan [3]int, 16)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() { defer wg.Done(); ch <- [3]int{a.State(), a.LastApplied(), a.Committed()} }()
	}
	wg.Wait()
	close(ch)
	same := true
	first := true
	var want [3]int
	for v := range ch {
		if first {
			want, first = v, false
		} else if v != want {
			same = false
		}
	}
	report(same, "16 concurrent readers agree on (st,la,c)=%v", want)

	if fail {
		os.Exit(1)
	}
}

func equal(x, y [][3]int) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func mkLog(m int) *api.API {
	b := api.New()
	for i := 0; i < m; i++ {
		_ = b.Append(sm.Add(1))
	}
	return b
}
