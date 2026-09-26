// Command demo runs acceptance checks for the Raft log-replication bookkeeping.
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
)

var failed bool

func check(name string, cond bool) {
	status := "OK"
	if !cond {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", name, status)
}

func main() {
	c := api.New(3)
	must := func(err error) {
		if err != nil {
			fmt.Println("FAIL setup:", err)
			os.Exit(1)
		}
	}
	must(c.Elect(1))
	for i := 0; i < 5; i++ {
		must(c.Append(1))
	}
	must(c.Elect(5)) // spec start state: term 5, log terms [1,1,1,1,1]
	// Two legal rejection hints (r=0) back nextIndex off to the spec's
	// initial next=[1,1] (Elect alone would leave it at Len()+1).
	must(c.Replicate(2, false, 0))
	must(c.Replicate(3, false, 0))

	// The eight steps of NOTES.md, each checked against the derived table.
	steps := []struct {
		run                func() error
		m2, m3, n2, n3, cm int
	}{
		{func() error { return c.Replicate(2, true, 5) }, 5, 0, 6, 1, 0},
		{func() error { return c.Replicate(3, true, 5) }, 5, 5, 6, 6, 0},
		{func() error { return c.Append(5) }, 5, 5, 6, 6, 0},
		{func() error { return c.Replicate(2, true, 6) }, 6, 5, 7, 6, 6},
		{func() error { return c.Replicate(3, true, 6) }, 6, 6, 7, 7, 6},
		{func() error { return c.Elect(6) }, 0, 0, 7, 7, 6},
		{func() error { return c.Replicate(2, true, 6) }, 6, 0, 7, 7, 6},
		{func() error { return c.Replicate(3, false, 2) }, 6, 0, 7, 3, 6},
	}
	for i, s := range steps {
		must(s.run())
		got := []int{c.MatchIndex(2), c.MatchIndex(3), c.NextIndex(2), c.NextIndex(3), c.CommitIndex()}
		want := []int{s.m2, s.m3, s.n2, s.n3, s.cm}
		check(fmt.Sprintf("S%d commit=%d match=[%d,%d] next=[%d,%d]", i+1, got[4], got[0], got[1], got[2], got[3]),
			slices.Equal(got, want))
	}

	// Four decidable, mutually distinct rejection kinds; no state may change.
	before := []int{c.CommitIndex(), c.MatchIndex(2), c.MatchIndex(3), c.NextIndex(2), c.NextIndex(3)}
	errs := []error{
		c.Replicate(1, true, 0),  // follower index: the leader itself
		c.Append(4),              // append term != current term (6)
		c.Elect(6),               // election term not greater
		c.Replicate(2, true, 99), // hint r beyond log length
	}
	sents := []error{api.ErrFollowerIndex, api.ErrTermMismatch, api.ErrStaleTerm, api.ErrHintRange}
	distinct := true
	for i := range errs {
		if !errors.Is(errs[i], sents[i]) {
			distinct = false
		}
		for j := i + 1; j < len(sents); j++ {
			if errors.Is(sents[i], sents[j]) {
				distinct = false
			}
		}
	}
	after := []int{c.CommitIndex(), c.MatchIndex(2), c.MatchIndex(3), c.NextIndex(2), c.NextIndex(3)}
	check("4 distinct sentinel errors, rejected ops leave no trace",
		distinct && slices.Equal(before, after) && c.Replicate(3, true, 6) == nil)

	// Concurrent read-only access must observe identical values (no sleeps).
	want := []int{c.CommitIndex(), c.MatchIndex(2), c.MatchIndex(3)}
	var wg sync.WaitGroup
	var consistent atomic.Bool
	consistent.Store(true)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 2000; k++ {
				got := []int{c.CommitIndex(), c.MatchIndex(2), c.MatchIndex(3)}
				if !slices.Equal(got, want) {
					consistent.Store(false)
				}
			}
		}()
	}
	wg.Wait()
	// SelfCheck covers invariants 1-4 and the O(1)-reads proof for m up to 10000.
	check("selfcheck(invariants 1-4, O(1) reads) + concurrent readers",
		api.New(3).SelfCheck() == nil && consistent.Load())

	if failed {
		os.Exit(1)
	}
}
