package main

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"

	"ontology/snapshot"
	"ontology/txn"
	"ontology/visible"
)

func main() {
	fails := 0
	report := func(name string, ok bool) {
		if !ok {
			fails++
			fmt.Println("FAIL", name)
			return
		}
		fmt.Println("OK", name)
	}
	tab := txn.NewTable()
	_, err := tab.Status(42)
	report("txn: unknown id is ErrUnknownTxn", errors.Is(err, txn.ErrUnknownTxn))
	_, err = snapshot.New(tab, 1, 0, make([]txn.ID, snapshot.MaxActive+1))
	report("snapshot: oversized active set rejected", errors.Is(err, snapshot.ErrTooManyActive))
	for id := txn.ID(1); id <= 4; id++ {
		tab.Begin(id)
	}
	_, _, _ = tab.Commit(1, 5), tab.Commit(2, 10), tab.Abort(4) // T1: active yet seq 5 < hi 10; T2: seq == hi; T4: aborted
	snap, _ := snapshot.New(tab, 10, 3, []txn.ID{1, 3})
	var ch visible.Checker
	check := func(id txn.ID) bool { v, _ := ch.Check(snap, id); return v }
	e1, _ := tab.Status(1)
	report("counterexample: naive=visible, impl=invisible", e1.CommitSeq < snap.HiWater() && !check(1))
	report("commit seq == hi-water is invisible", !check(2))
	report("own write is visible to itself", check(3))
	report("aborted txn is never visible", !check(4))
	report("single check uses <= 2 lookups", ch.Lookups() <= 2)
	report("10000 random cases match naive reference", randomCheck())
	report("concurrent checks agree", concurrentCheck())
	report(fmt.Sprintf("total: %d failure(s)", fails), fails == 0)
}
func randomCheck() bool {
	tab := txn.NewTable()
	for id := txn.ID(1); id <= 500; id++ {
		tab.Begin(id)
		if r := rand.Intn(3); r == 0 {
			_ = tab.Commit(id, uint64(id))
		} else if r == 1 {
			_ = tab.Abort(id)
		}
	}
	var ch visible.Checker
	for i := 0; i < 10000; i++ {
		active := make([]txn.ID, 20)
		for j := range active {
			active[j] = txn.ID(rand.Intn(500) + 1)
		}
		snap, _ := snapshot.New(tab, uint64(rand.Intn(600)), 0, active)
		id := txn.ID(rand.Intn(600))
		got, gerr := ch.Check(snap, id)
		want, werr := visible.NaiveCheck(snap, id)
		if got != want || (gerr == nil) != (werr == nil) {
			return false
		}
	}
	return true
}
func concurrentCheck() bool {
	tab := txn.NewTable()
	for id := txn.ID(1); id <= 1000; id++ {
		tab.Begin(id)
		_ = tab.Commit(id, uint64(id))
	}
	active := make([]txn.ID, 100)
	for i := range active {
		active[i] = txn.ID(i + 1)
	}
	snap, _ := snapshot.New(tab, 500, 0, active)
	var wg sync.WaitGroup
	bad := make(chan struct{}, 1600)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var ch visible.Checker
			for id := txn.ID(1); id <= 200; id++ {
				v, _ := ch.Check(snap, id)
				if nv, _ := visible.NaiveCheck(snap, id); v != nv || ch.Lookups() > 2 {
					bad <- struct{}{}
				}
			}
		}()
	}
	wg.Wait()
	return len(bad) == 0
}
