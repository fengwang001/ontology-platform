// Command demo exercises the offset unwrap tracker: no args, no network.
// It prints OK/FAIL lines and exits 0 only when every check passes.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"sync"

	"ontology/api"
	"ontology/wrap"
)

const threshold = 1<<31 - 1 // largest legal threshold; same verdicts as 2^31 here

var failed bool

func ok(cond bool, msg string) {
	if cond {
		fmt.Println("OK", msg)
	} else {
		failed = true
		fmt.Println("FAIL", msg)
	}
}

func main() {
	// Section 3: the eight steps through the public api.
	raw := []uint32{100, 200, 4294967200, 50, 60, 40, 4294967295, 5}
	names := [...]string{"First", "Forward", "Wrap", "Dup"}
	t, _ := api.New(threshold)
	events := make([]string, 0, len(raw))
	unwrapped := make([]int64, 0, len(raw))
	rejected := false
	for _, r := range raw {
		ev, ferr := t.Feed(r)
		u, _ := t.LastUnwrapped()
		if errors.Is(ferr, api.ErrRegression) {
			rejected, events = true, append(events, "Reg")
		} else {
			events = append(events, names[ev])
		}
		unwrapped = append(unwrapped, u)
	}
	ok(fmt.Sprint(events) == "[First Forward Forward Wrap Forward Reg Forward Wrap]",
		"8-step events "+fmt.Sprint(events))
	ok(unwrapped[3] == 4294967346 && unwrapped[7] == 8589934597,
		"step4/8 wrap unwrapped "+fmt.Sprint(unwrapped))
	ok(rejected && unwrapped[5] == 4294967356, "step6 regression rejected, pu kept")

	// Four distinct, decidable sentinel errors.
	_, e1 := api.New(0)
	_, e2 := api.New(1 << 31)
	empty, _ := api.New(threshold)
	_, e3 := empty.LastUnwrapped()
	_, e4 := empty.LastRaw()
	rg, _ := api.New(threshold)
	rg.Feed(100)
	_, e5 := rg.Feed(50)
	_, e6 := wrap.Unwrap(math.MaxInt64-50, 0, 100, api.Forward)
	ok(errors.Is(e1, api.ErrInvalidThreshold) && errors.Is(e2, api.ErrInvalidThreshold) &&
		errors.Is(e3, api.ErrEmpty) && errors.Is(e4, api.ErrEmpty) &&
		errors.Is(e5, api.ErrRegression) && errors.Is(e6, api.ErrOverflow),
		"four distinct sentinel errors")

	// Rejection leaves no trace; the tracker keeps working.
	r0, _ := rg.LastRaw()
	ev, contErr := rg.Feed(200)
	cu, _ := rg.LastUnwrapped()
	ok(r0 == 100 && rg.Counts()[api.First] == 1 && contErr == nil &&
		ev == api.Forward && cu == 200, "reject leaves no trace, still usable")

	ok(t.SelfCheck() == nil, "SelfCheck four invariants")

	// O(1) path at several m scales (numeric comparisons==1 is asserted in
	// the same-package eng test; the field never appears in any public API).
	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		b, _ := api.New(threshold)
		for i := 1; i <= m+1; i++ {
			if _, err := b.Feed(uint32(i)); err != nil {
				bigOK = false
			}
		}
		if got, _ := b.LastUnwrapped(); got != int64(m+1) {
			bigOK = false
		}
	}
	ok(bigOK, "constant-compare path for m=100,1000,10000")

	// Concurrent readers see identical snapshots; no sleeps.
	const n = 8
	type snap struct {
		u int64
		c map[api.Event]int
	}
	got := make([]snap, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 64; j++ {
				u, _ := t.LastUnwrapped()
				got[i] = snap{u, t.Counts()}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	concOK := true
	for i := 1; i < n; i++ {
		if got[i].u != got[0].u || !reflect.DeepEqual(got[i].c, got[0].c) {
			concOK = false
		}
	}
	ok(concOK, "concurrent readers identical")

	if failed {
		os.Exit(1)
	}
}
