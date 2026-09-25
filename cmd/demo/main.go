// Command demo exercises the append-only audit log end to end.
package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/ent"
	"ontology/log"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	g := ent.Genesis()
	check("ent: genesis fixed hash", g.Seq == 0 && g.Hash == ent.GenesisHash())

	// The seven Append requests from NOTES.md, via the public facade.
	a := api.New()
	steps := []struct {
		ts   int64
		who  string
		op   string
		want int64 // -1 means reject
	}{
		{10, "A", "put", 1}, {12, "A", "get", 2}, {12, "B", "del", 3},
		{9, "A", "put", -1}, {15, "B", "put", 4}, {15, "", "get", -1},
		{20, "A", "del", 5},
	}
	seqOK, rejOK := true, true
	for _, s := range steps {
		seq, err := a.Append(s.ts, s.who, s.op)
		if s.want < 0 {
			rejOK = rejOK && err != nil
		} else {
			seqOK = seqOK && err == nil && seq == s.want
		}
	}
	check("seven steps: seqs 1,2,3,_,4,_,5", seqOK)
	check("steps 4,6 rejected, 6 entries, no trace", rejOK && len(a.Entries()) == 6 && a.Verify() == -1)

	// Four distinguishable sentinel errors.
	cases := []struct {
		ts   int64
		who  string
		op   string
		want error
	}{{-1, "A", "put", log.ErrNegativeTS}, {1, "", "put", log.ErrEmptyWho},
		{1, "A", "", log.ErrEmptyOp}, {1, "A", "put", log.ErrOutOfOrder}}
	errOK := true
	for _, c := range cases {
		_, err := a.Append(c.ts, c.who, c.op)
		errOK = errOK && err == c.want
	}
	check("four distinct sentinel errors", errOK)

	// Tamper with Seq=3 (Op del->put, stored hashes untouched).
	es := a.Entries()
	es[3].Op = "put"
	t := log.NewFrom(es)
	check("tamper: Verify=3 Affected=[3 4 5]",
		t.Verify() == 3 && reflect.DeepEqual(t.Affected(3), []int64{3, 4, 5}))

	// Large m: append cost stays O(1) (pinned by TestAppendConstantWork).
	big := api.New()
	for i := int64(0); i < 10000; i++ {
		if _, err := big.Append(i, "u", "op"); err != nil {
			failed = true
		}
	}
	check("m=10000 appends, chain verifies", big.Verify() == -1)

	// Concurrent readers must all agree (no sleeps, start barrier).
	wantV, wantA := big.Verify(), big.Affected(9990)
	start := make(chan struct{})
	var wg sync.WaitGroup
	agree := true
	var mu sync.Mutex
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok := big.Verify() == wantV && reflect.DeepEqual(big.Affected(9990), wantA)
			mu.Lock()
			agree = agree && ok
			mu.Unlock()
		}()
	}
	close(start)
	wg.Wait()
	check("16 concurrent readers agree", agree)
	check("SelfCheck", api.New().SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
