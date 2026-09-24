// Command demo exercises the snapshot-visibility packages end to end.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"

	"ontology/snapshot"
	"ontology/txn"
	"ontology/visible"
)

var passed, total int

func check(name string, ok bool) {
	total++
	if ok {
		passed++
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
	}
}

func main() {
	tab := txn.NewTable()
	tab.Commit(1, 5)  // counterexample writer: seq 5 < water 10, still active
	tab.Commit(2, 10) // commit seq equal to the water mark
	tab.Abort(3)      // rolled back: never visible

	snap, _ := snapshot.New(10, 99, []txn.ID{1})
	naiveSaysVisible := uint64(5) < snap.Water() // wrong rule: seq below water
	got, _, _ := visible.Check(tab, snap, txn.Version{Txn: 1, Seq: 5})
	check("counterexample: naive visible, impl invisible", naiveSaysVisible && !got)

	got, _, _ = visible.Check(tab, snap, txn.Version{Txn: 2, Seq: 10})
	check("commit seq == water is invisible", !got)

	got, _, _ = visible.Check(tab, snap, txn.Version{Txn: 99})
	check("own writes are visible", got)

	got, _, _ = visible.Check(tab, snap, txn.Version{Txn: 3})
	check("aborted txn is never visible", !got)

	big := txn.NewTable()
	for i := txn.ID(1); i <= 10000; i++ {
		big.Commit(i, uint64(i))
	}
	active := make([]txn.ID, 1000)
	for i := range active {
		active[i] = txn.ID(i + 1)
	}
	bsnap, _ := snapshot.New(20000, 0, active)
	_, n, _ := visible.Check(big, bsnap, txn.Version{Txn: 5000})
	check("single judgment uses <= 2 lookups", n <= 2 && bsnap.Items() == 1000)

	tooMany := make([]txn.ID, snapshot.MaxActive+1)
	_, err := snapshot.New(1, 0, tooMany)
	check("oversized active set rejected", errors.Is(err, snapshot.ErrTooManyActive))

	rng := rand.New(rand.NewSource(1))
	same := true
	for i := 0; i < 10000 && same; i++ {
		s, _ := snapshot.New(uint64(rng.Intn(20000)), 0, active[:rng.Intn(1000)])
		v := txn.Version{Txn: txn.ID(rng.Intn(10000) + 1)}
		a, _, _ := visible.Check(big, s, v)
		b, _ := visible.NaiveCheck(big, s, v)
		same = a == b
	}
	check("10000 random cases match naive reference", same)

	var agree atomic.Bool
	agree.Store(true)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := txn.ID(1); i <= 10000 && agree.Load(); i++ {
				a, _, _ := visible.Check(big, bsnap, txn.Version{Txn: i})
				b, _ := visible.NaiveCheck(big, bsnap, txn.Version{Txn: i})
				if a != b {
					agree.Store(false)
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent judgments agree", agree.Load())

	fmt.Printf("TOTAL %d/%d OK\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
