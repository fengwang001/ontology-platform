// Command demo exercises the bitemporal store end to end. It uses only
// caller-supplied synthetic times (never the wall clock), prints one OK/FAIL
// line per check, and exits 0 when every check passes.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ontology"
)

var passed, total int

func check(ok bool, label string) {
	total++
	verdict := "OK  "
	if !ok {
		verdict = "FAIL "
	}
	if ok {
		passed++
	}
	fmt.Println(verdict + label)
}

func ts(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

func valueAt(s *ontology.Store, ent, prop string, validAt, txAt int64) (any, error) {
	f, err := s.AsOf(ent, prop, ts(validAt), ts(txAt))
	return f.Value, err
}

func main() {
	s := ontology.NewStore()
	_ = s.Write("e1", "name", "alice", ts(100), ts(200), ts(10))

	v, err := valueAt(s, "e1", "name", 100, 10)
	check(err == nil && v == "alice", "boundary: validAt==from hits")
	_, err = valueAt(s, "e1", "name", 200, 10)
	check(errors.Is(err, ontology.ErrValidOutOfRange), "boundary: validAt==to misses")

	err = s.Write("e2", "p", "x", ts(5), ts(5), ts(1))
	check(errors.Is(err, ontology.ErrEmptyInterval) && !errors.Is(err, ontology.ErrInvertedInterval),
		"empty interval: ErrEmptyInterval (distinct)")
	err = s.Write("e2", "p", "x", ts(6), ts(5), ts(1))
	check(errors.Is(err, ontology.ErrInvertedInterval) && !errors.Is(err, ontology.ErrEmptyInterval),
		"inverted interval: ErrInvertedInterval (distinct)")

	sp := ontology.NewStore()
	_ = sp.Write("e3", "p", "old", ts(0), ts(100), ts(1))
	_ = sp.Write("e3", "p", "new", ts(30), ts(60), ts(2))
	e29, _ := valueAt(sp, "e3", "p", 29, 2)
	e30, _ := valueAt(sp, "e3", "p", 30, 2)
	e59, _ := valueAt(sp, "e3", "p", 59, 2)
	e60, _ := valueAt(sp, "e3", "p", 60, 2)
	check(e29 == "old" && e30 == "new" && e59 == "new" && e60 == "old",
		"split: residuals [0,30) and [60,100), edges exact")

	tr := ontology.NewStore()
	_ = tr.Write("e4", "p", "v1", ts(0), ts(100), ts(1))
	_ = tr.Write("e4", "p", "v2", ts(0), ts(100), ts(2))
	_ = tr.Write("e4", "p", "v3", ts(0), ts(100), ts(3))
	a1, _ := valueAt(tr, "e4", "p", 50, 1)
	a2, _ := valueAt(tr, "e4", "p", 50, 2)
	a3, _ := valueAt(tr, "e4", "p", 50, 3)
	corr, _ := tr.Corrections("e4", "p", ts(50))
	check(a1 == "v1" && a2 == "v2" && a3 == "v3" && len(corr) == 3,
		"trajectory at validAt=50: v1 -> v2 -> v3 (tx-ordered)")

	_, err = valueAt(s, "ghost", "p", 1, 1)
	check(errors.Is(err, ontology.ErrNoFacts), "not-found: never any fact")
	_, err = valueAt(s, "e1", "name", 999, 10)
	check(errors.Is(err, ontology.ErrValidOutOfRange), "not-found: validAt outside intervals")
	_, err = valueAt(s, "e1", "name", 150, 5)
	check(errors.Is(err, ontology.ErrNotYetKnown), "not-found: not yet known at txAt")

	before, _ := valueAt(s, "e1", "name", 150, 10)
	err = s.Write("e1", "name", "bob", ts(120), ts(180), ts(9))
	after, _ := valueAt(s, "e1", "name", 150, 10)
	check(errors.Is(err, ontology.ErrTxRegression) && before == after,
		"tx regression rejected, state unchanged")

	const writers = 8
	var seq atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for errors.Is(s.Write("e5", "p", "x", ts(0), ts(10), ts(1000+seq.Add(1))),
				ontology.ErrTxRegression) {
			}
		}()
	}
	wg.Wait()
	series, _ := s.Corrections("e5", "p", ts(5))
	strict := len(series) == writers
	for i := 1; i < len(series); i++ {
		strict = strict && series[i].TxFrom.After(series[i-1].TxFrom)
	}
	check(strict, "concurrent same-entity writes: tx series strictly increasing")

	fmt.Printf("TOTAL %d/%d passed\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
