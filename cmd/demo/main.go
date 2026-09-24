// Command demo exercises the snapshot-visibility packages end to end.
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"ontology/snapshot"
	"ontology/txn"
	"ontology/visible"
)

var fails int

func check(name string, ok bool) {
	state := "OK  "
	if !ok {
		state, fails = "FAIL ", fails+1
	}
	fmt.Println(state, name)
}

// naiveScan is the demo's independent reference implementation.
func naiveScan(recs map[txn.ID]txn.Record, s *snapshot.Snapshot, id txn.ID) bool {
	if id == s.Owner() {
		return true
	}
	rec, ok := recs[id]
	return ok && rec.Status == txn.Committed && rec.Commit < s.Watermark() && !s.InActive(id)
}

func main() {
	recs := map[txn.ID]txn.Record{
		1: {Status: txn.Committed, Commit: 5}, 2: {Status: txn.Committed, Commit: 10},
		3: {Status: txn.Aborted}, 4: {Status: txn.Active},
	}
	tab := txn.NewTable(recs)
	snap, _ := snapshot.New(10, 4, []txn.ID{1, 4})
	see := func(id txn.ID) bool { _, v, _ := visible.Judge(tab, snap, []txn.ID{id}); return v }
	naive := recs[1].Commit < snap.Watermark()
	check("counterexample: naive sees it, impl does not", naive && !see(1))
	check("commit == watermark is invisible", !see(2))
	check("aborted is never visible", !see(3))
	check("own write is visible to itself", see(4))
	_, _, n, _ := visible.JudgeCount(tab, snap, []txn.ID{1})
	check("single judgement uses <= 2 lookups", n <= 2)
	_, err := snapshot.New(1, 0, make([]txn.ID, snapshot.MaxActive+1))
	check("oversized active set rejected", errors.Is(err, snapshot.ErrTooManyActive))
	r := rand.New(rand.NewSource(1))
	gen := make(map[txn.ID]txn.Record)
	for i := 1; i <= 500; i++ {
		gen[txn.ID(i)] = txn.Record{Status: txn.Status(r.Intn(3)), Commit: uint64(r.Intn(300))}
	}
	gtab := txn.NewTable(gen)
	same := true
	for k := 0; k < 10000 && same; k++ {
		var active []txn.ID
		for i := 1; i <= 500; i++ {
			if r.Intn(20) == 0 {
				active = append(active, txn.ID(i))
			}
		}
		s, _ := snapshot.New(uint64(r.Intn(300)), txn.ID(r.Intn(501)), active)
		id := txn.ID(r.Intn(500) + 1)
		_, got, _ := visible.Judge(gtab, s, []txn.ID{id})
		same = got == naiveScan(gen, s, id)
	}
	check("10000 random cases match naive reference", same)
	want := []bool{see(1), see(2), see(3), see(4)}
	got := make([][]bool, 8)
	var wg sync.WaitGroup
	for g := range got {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			got[g] = make([]bool, 400)
			for i := range got[g] {
				got[g][i] = see(txn.ID(i%4 + 1))
			}
		}(g)
	}
	wg.Wait()
	agree := true
	for _, gs := range got {
		for i, v := range gs {
			agree = agree && v == want[i%4]
		}
	}
	check("concurrent judgements agree", agree)
	if fails > 0 {
		fmt.Printf("FAIL total: %d check(s) failed\n", fails)
		os.Exit(1)
	}
	fmt.Println("OK   total: all checks passed")
}
