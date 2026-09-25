package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/demux"
	"ontology/slot"
)

var failed bool

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// slot: min-free allocation, generation bump, log-bound on inspected slots
	t := slot.New(10000)
	ok := true
	var handles []slot.Handle
	for i := 0; i < 5000; i++ {
		h, err := t.Open()
		if err != nil || h.ID != i || h.Gen != 1 {
			ok = false
		}
		handles = append(handles, h)
	}
	for i := 0; i < 5000; i += 2 { // close even ids, reopen: must reuse in min order
		t.Free(handles[i].ID)
	}
	for i := 0; i < 2500; i++ {
		h, err := t.Open()
		if err != nil || h.ID != 2*i || h.Gen != 2 {
			ok = false
		}
	}
	report("slot min-free reuse + gen bump", ok)
	report("slot open checks <= ceil(log2 C)+1", t.BoundOK())

	// demux: the eight-step walkthrough from NOTES.md, C=4
	d := demux.New(4)
	h0, _ := d.Open()
	h1, _ := d.Open()
	h2, _ := d.Open()
	eClose := d.Close(h1)                      // step 4
	h1b, eOpen := d.Open()                     // step 5: reuses id 1 with gen 2
	eStale := d.Recv(1, h1.Gen, []byte("old")) // step 6
	eHalf := d.Recv(3, h1.Gen, []byte("x"))    // step 7
	eDeliv := d.Recv(1, h1b.Gen, []byte("hi")) // step 8
	walk := h0 == (demux.Handle{ID: 0, Gen: 1}) &&
		h1 == (demux.Handle{ID: 1, Gen: 1}) && h2 == (demux.Handle{ID: 2, Gen: 1}) &&
		eClose == nil && h1b == (demux.Handle{ID: 1, Gen: 2}) && eOpen == nil &&
		errors.Is(eStale, demux.ErrStale) && errors.Is(eHalf, demux.ErrHalfOpen) &&
		eDeliv == nil && string(d.Data(1)) == "hi" && d.Gen(1) == 2
	report("demux 8-step: stale(6)/half-open(7)/delivered(8)", walk)
	report("demux generation isolation: no old-gen cross-stream",
		string(d.Data(1)) == "hi" && len(d.Data(3)) == 0)

	// api: four distinguishable sentinel errors, rejects leave no trace
	a := api.New(2)
	full, _ := a.Open()
	full2, _ := a.Open()
	_, eNo := a.Open()
	eBad := a.Recv(7, 1, nil)
	eSt := a.Recv(0, full.Gen+1, nil) // gen mismatch on OPEN slot -> stale
	eHo := error(nil)
	{
		b := api.New(2)
		if _, err := b.Open(); err != nil {
			failed = true
		}
		eHo = b.Recv(1, 1, nil) // id 1 never opened -> half-open
	}
	four := errors.Is(eNo, api.ErrNoSlots) && errors.Is(eBad, api.ErrBadID) &&
		errors.Is(eHo, api.ErrHalfOpen) && errors.Is(eSt, api.ErrStale) &&
		eNo != eBad && eBad != eHo && eHo != eSt
	report("api four distinguishable sentinel errors", four)
	before := string(a.Data(0)) + string(a.Data(1))
	_ = a.Close(api.Handle{ID: 0, Gen: 42}) // stale handle: rejected
	_ = a.Recv(5, 1, []byte("z"))           // bad id: rejected
	notrace := a.Gen(0) == full.Gen && a.Gen(1) == full2.Gen &&
		string(a.Data(0))+string(a.Data(1)) == before &&
		a.Close(full) == nil && a.Close(full2) == nil // still usable
	report("api rejected ops leave no trace, still usable", notrace)
	report("api SelfCheck", api.New(4).SelfCheck() == nil)

	// concurrency: N goroutines open+send, main recvs; no cross-stream
	const n = 64
	c := api.New(n)
	hs := make([]api.Handle, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h, err := c.Open()
			if err == nil && c.Send(h, []byte{byte(i)}) == nil {
				hs[i] = h
			}
		}(i)
	}
	wg.Wait()
	seen := map[api.Handle]bool{}
	conc := true
	for i, h := range hs {
		if seen[h] {
			conc = false
		}
		seen[h] = true
		if c.Recv(h.ID, h.Gen, []byte{byte(i)}) != nil ||
			string(c.Data(h.ID)) != string([]byte{byte(i)}) {
			conc = false
		}
	}
	report("concurrent open/send/recv: unique handles, no crosstalk", conc && len(seen) == n)

	if failed {
		os.Exit(1)
	}
}
