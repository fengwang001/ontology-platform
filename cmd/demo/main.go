// Command demo verifies the read-committed materialized view behavior.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/mvcc"
	"ontology/ver"
)

var failed bool

func check(name string, good bool) {
	if !good {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK", false: "FAIL"}[good], name)
}

func main() {
	// ver: pending overwrite; committed entry keeps greatest seq; absence.
	p := ver.NewPending()
	p.Put("X", "9")
	p.Put("X", "7")
	pv, pok := p.Get("X")
	_, pmiss := p.Get("Z")
	e := ver.Entry{}
	e.Apply("1", 1)
	e.Apply("9", 5)
	e.Apply("2", 2)
	ev, eok := e.Latest()
	_, absent := ver.Entry{}.Latest()
	check("ver pending/entry", pok && pv == "7" && !pmiss && eok && ev == "9" && !absent)
	// mvcc: the 15-step NOTES scenario, reads at steps 8/11/12/14/15.
	s := mvcc.New()
	t1 := s.Begin()
	_ = s.Write(t1, "X", "1")
	_ = s.Commit(t1)
	t2 := s.Begin()
	_ = s.Write(t2, "Y", "2")
	_ = s.Commit(t2)
	t3 := s.Begin()
	r8, _, _ := s.ReadTx(t3, "X")
	t4 := s.Begin()
	_ = s.Write(t4, "X", "5")
	r11, _, _ := s.ReadTx(t3, "X")
	r12, _, _ := s.ReadTx(t4, "X")
	_ = s.Commit(t4)
	r14, _, _ := s.ReadTx(t3, "X")
	r15, _, _ := s.ReadTx(t3, "Y")
	check("steps 8,11,12,14,15 => 1,1,5,5,2", r8 == "1" && r11 == "1" && r12 == "5" && r14 == "5" && r15 == "2")
	// Non-repeatable read: the same tx later sees a newer commit.
	s = mvcc.New()
	tx := s.Begin()
	c1 := s.Begin()
	_ = s.Write(c1, "X", "1")
	_ = s.Commit(c1)
	v1, ok1, _ := s.ReadTx(tx, "X")
	c2 := s.Begin()
	_ = s.Write(c2, "X", "9")
	_ = s.Commit(c2)
	v2, _, _ := s.ReadTx(tx, "X")
	check("non-repeatable read", ok1 && v1 == "1" && v2 == "9")
	// An uncommitted write is invisible to Read and to another tx.
	s = mvcc.New()
	td := s.Begin()
	_ = s.Write(td, "X", "1")
	_, iok := s.Read("X")
	_, rok, _ := s.ReadTx(s.Begin(), "X")
	check("uncommitted invisible", !iok && !rok)
	// A tx sees its own uncommitted write.
	s = mvcc.New()
	to := s.Begin()
	_ = s.Write(to, "X", "7")
	ov, ook, _ := s.ReadTx(to, "X")
	check("own write visible", ook && ov == "7")
	// Four distinct, decidable sentinel errors.
	s = mvcc.New()
	tc := s.Begin()
	eKey := s.Write(tc, "", "v")
	eVal := s.Write(tc, "k", "")
	eNB := s.Write(99999, "k", "v")
	_ = s.Commit(tc)
	eDone := s.Write(tc, "a", "b")
	distinct := mvcc.ErrEmptyKey != mvcc.ErrEmptyValue && mvcc.ErrEmptyKey != mvcc.ErrTxNotBegun &&
		mvcc.ErrEmptyKey != mvcc.ErrTxCommitted && mvcc.ErrEmptyValue != mvcc.ErrTxNotBegun &&
		mvcc.ErrEmptyValue != mvcc.ErrTxCommitted && mvcc.ErrTxNotBegun != mvcc.ErrTxCommitted
	check("four distinct sentinel errors", distinct && errors.Is(eKey, mvcc.ErrEmptyKey) &&
		errors.Is(eVal, mvcc.ErrEmptyValue) && errors.Is(eNB, mvcc.ErrTxNotBegun) &&
		errors.Is(eDone, mvcc.ErrTxCommitted))
	// Rejected ops leave no trace; the store stays usable afterwards.
	s = mvcc.New()
	tr := s.Begin()
	_ = s.Write(tr, "K", "")
	_ = s.Write(tr, "", "x")
	_, rmiss, _ := s.ReadTx(tr, "K")
	_ = s.Write(99999, "K", "z")
	_, cMiss := s.Read("K")
	_ = s.Commit(tr)
	badCommit := s.Commit(tr) // second commit of the same tx -> rejected
	tx2 := s.Begin()
	_ = s.Write(tx2, "K", "ok")
	_ = s.Commit(tx2)
	rv, rok2 := s.Read("K")
	check("rejected ops leave no trace", !rmiss && !cMiss &&
		errors.Is(badCommit, mvcc.ErrTxCommitted) && rok2 && rv == "ok")
	// O(1): inspected version count does not grow with m.
	check("read O(1) over m=100..10000", mvcc.New().CheckReadCost() == nil)
	// Concurrent readers of the same keys agree key-by-key.
	s = mvcc.New()
	const nKeys, nReaders = 100, 16
	ref := make([]string, nKeys)
	for i := range ref {
		ref[i] = fmt.Sprintf("v%d", i)
		t := s.Begin()
		_ = s.Write(t, fmt.Sprintf("k%d", i), ref[i])
		_ = s.Commit(t)
	}
	var bad atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < nReaders; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < 50; round++ {
				for i, want := range ref {
					if got, ok := s.Read(fmt.Sprintf("k%d", i)); !ok || got != want {
						bad.Store(true)
					}
				}
			}
		}()
	}
	wg.Wait()
	check("concurrent readers agree", !bad.Load())
	// Public facade self-check of all four invariants.
	check("api SelfCheck", api.New().SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
