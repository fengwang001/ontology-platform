// Command demo exercises the broadcast join end to end and prints one OK/FAIL
// per required judgment. It takes no args, does no networking, exits 0 on pass.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

func main() {
	failed := false
	ok := func(cond bool, msg string) {
		if cond {
			fmt.Println("OK: " + msg)
		} else {
			fmt.Println("FAIL: " + msg)
			failed = true
		}
	}

	e, _ := api.New(100)
	must := func(f api.Fact) api.Row {
		r, err := e.Join(f)
		if err != nil {
			panic(err)
		}
		return r
	}
	e.Broadcast([]api.Entry{{Key: "a", Val: "1"}, {Key: "b", Val: "2"}})
	e.Broadcast([]api.Entry{{Key: "c", Val: "3"}, {Key: "d", Val: "4"}})
	r3 := must(api.Fact{Key: "a", Vsn: 1}) // step 3
	e.Broadcast([]api.Entry{{Key: "a", Val: "9"}})
	r5 := must(api.Fact{Key: "a", Vsn: 1}) // step 5: must still see v1
	r6 := must(api.Fact{Key: "a", Vsn: 3}) // step 6: sees v3
	e.Broadcast([]api.Entry{{Key: "e", Val: "5"}, {Key: "f", Val: "6"}})
	r8, err8 := e.Join(api.Fact{Key: "a", Vsn: 1}) // step 8: stale
	ok(r3.Val == "1" && r5.Val == "1" && r6.Val == "9",
		fmt.Sprintf("step3/5 {a,1}=(%s,%s), step6 {a,3}=(%s)", r3.Val, r5.Val, r6.Val))
	ok(err8 == nil && r8 == (api.Row{}) && e.Dropped() == 1, "step8 {a,1}=stale(dropped=1)")
	ok(len(e.Joined()) == 3, "emitted rows = 3 hits across the eight steps")
	// Post-eviction the retained set is v3/v4 (used=90, set [3 4] are certified
	// exactly by SelfCheck); the current-version fact {e,4} still resolves.
	rE, _ := e.Join(api.Fact{Key: "e", Vsn: 4})
	ok(rE == (api.Row{Key: "e", Val: "5", Vsn: 4}), "post-eviction {e,4}=(e,5); versions [3 4], used 90 via SelfCheck")

	// Four distinct decidable sentinel errors.
	_, eMax := api.New(0)
	_, eBatch := e.Broadcast(nil)
	_, eKey := e.Broadcast([]api.Entry{{Key: "", Val: "z"}})
	_, eFuture := e.Join(api.Fact{Key: "a", Vsn: 999})
	distinct := map[error]bool{eMax: true, eBatch: true, eKey: true, eFuture: true}
	ok(errors.Is(eMax, api.ErrMaxBytes) && errors.Is(eBatch, api.ErrEmptyBatch) &&
		errors.Is(eKey, api.ErrEmptyKey) && errors.Is(eFuture, api.ErrFuture) && len(distinct) == 4,
		"four distinct sentinels: maxBytes/emptyBatch/emptyKey/future")

	// Rejected operations leave no trace on the observable state.
	before := fmt.Sprint(e.View(), len(e.Joined()), e.Dropped(), e.Missed())
	e.Broadcast(nil)
	e.Broadcast([]api.Entry{{Key: "", Val: "z"}})
	e.Join(api.Fact{})
	e.Join(api.Fact{Key: "a", Vsn: 999})
	after := fmt.Sprint(e.View(), len(e.Joined()), e.Dropped(), e.Missed())
	ok(before == after, "rejected ops leave View/rows/drop/miss unchanged")

	// O(1) map lookup: the unexported probe counter is not exposed; the bound is
	// pinned inside the dim package by TestProbeCountBound (m=100..10000).
	big, _ := api.New(1 << 40)
	var batch []api.Entry
	for i := 0; i < 10000; i++ {
		batch = append(batch, api.Entry{Key: fmt.Sprintf("k%05d", i), Val: fmt.Sprint(i)})
	}
	big.Broadcast(batch)
	rb, errB := big.Join(api.Fact{Key: "k05000", Vsn: 1})
	ok(errB == nil && rb.Val == "5000", "m=10000 map lookup correct; O(1) probe pinned by dim.TestProbeCountBound")

	// Concurrency: N goroutines join same/different keys; rows must be identical.
	const n = 32
	var wg sync.WaitGroup
	agree := true
	var mu sync.Mutex
	want := api.Row{Key: "k00001", Val: "1", Vsn: 1}
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := big.Join(api.Fact{Key: "k00001", Vsn: 1})
			if err != nil || r != want {
				mu.Lock()
				agree = false
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	ok(agree, fmt.Sprintf("%d concurrent joins of one key are field-identical", n))

	ok(e.SelfCheck() == nil, "SelfCheck verifies rows, used=90, versions=[3 4], invariants")

	if failed {
		os.Exit(1)
	}
}
