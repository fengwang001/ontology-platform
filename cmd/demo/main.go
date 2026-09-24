// Command demo runs in-process self-checks for the temporal join packages.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/tjoin"
	"ontology/ver"
)

var fails int

func ok(name string, pass bool) {
	if pass {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
		fails++
	}
}

func main() {
	var s ver.Versions
	s.Put(ver.Version{ValidFrom: 10, Value: "A"})
	s.Put(ver.Version{ValidFrom: 20, Value: "B"})
	v, found := s.AsOf(10) // ValidFrom exactly equal to event time
	ok("ver boundary ValidFrom==TS", found && v.Value == "A")
	s.Put(ver.Version{ValidFrom: 22, Tombstone: true})
	v, found = s.AsOf(25) // inside the tombstone's interval: lookup hit, no value
	ok("ver tombstone interval", found && v.Tombstone)
	ok("ver log probe bound", ver.VerifyProbeBound())

	// Thirteen-step scenario from NOTES.md; collect step 9/10/13 outcomes.
	t := tjoin.New(10)
	must := func(err error) {
		if err != nil {
			fmt.Println("FAIL thirteen steps unexpected error:", err)
			fails++
		}
	}
	must(t.Upsert("k", 10, "A"))
	_, err := t.Feed("k", 12)
	must(err) // e1
	must(t.Upsert("k", 20, "B"))
	_, err = t.Feed("k", 25)
	must(err) // e2
	out5, err := t.Watermark(11)
	must(err)
	must(t.Upsert("k", 12, "C"))
	_, err = t.Feed("k", 22)
	must(err) // e3
	must(t.Delete("k", 22))
	out9, err := t.Watermark(22)
	must(err)
	err10 := t.Upsert("k", 22, "X") // rejected: late
	_, err = t.Feed("k", 8)
	must(err) // e4 immediate
	must(t.Upsert("k", 30, "D"))
	out13, err := t.Watermark(30)
	must(err)
	step9 := len(out5) == 0 && len(out9) == 2 &&
		out9[0].Seq == 0 && out9[0].Found && out9[0].Value == "C" &&
		out9[1].Seq == 2 && !out9[1].Found && out9[1].Value == ""
	step10 := errors.Is(err10, tjoin.ErrLateVersion)
	step13 := len(out13) == 1 && out13[0].Seq == 1 && !out13[0].Found
	ok("tjoin 13 steps (9:e1->C,e3 none; 10 rejected; 13:e2 none)", step9 && step10 && step13)

	ok("api SelfCheck", api.New(10).SelfCheck())

	// Four distinguishable sentinels; rejected ops leave no state behind.
	j := api.New(1)
	_, eEmpty := j.Feed("", 1)
	eEmpty2 := j.Delete("", 1)
	_, _ = j.Feed("k", 10)
	_, eFull := j.Feed("k", 11)
	_, _ = j.Watermark(5)
	eLate := j.Upsert("k", 5, "x")
	_, eBack := j.Watermark(4)
	sentinels := errors.Is(eEmpty, api.ErrEmptyKey) && errors.Is(eEmpty2, api.ErrEmptyKey) &&
		errors.Is(eFull, api.ErrBufferFull) && errors.Is(eLate, api.ErrLateVersion) &&
		errors.Is(eBack, api.ErrWatermarkBack) &&
		!errors.Is(api.ErrEmptyKey, api.ErrLateVersion) && !errors.Is(api.ErrBufferFull, api.ErrWatermarkBack)
	j.Flush()
	outs := j.Outputs() // only the accepted event (seq 0) may appear
	ok("api 4 sentinels + rejected leaves no state", sentinels && len(outs) == 1 && outs[0].Seq == 0 && !outs[0].Found)

	// Flush agrees with naive AS OF; concurrent Feed emits exactly once.
	j2 := api.New(1 << 16)
	must(j2.Upsert("a", 0, "a0"))
	must(j2.Upsert("a", 10, "a10"))
	must(j2.Delete("a", 20))
	const G, N = 8, 64
	var wg sync.WaitGroup
	var feedErrs atomic.Int64
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < N; i++ {
				if _, err := j2.Feed("a", int64((g*N+i)*3%40)); err != nil {
					feedErrs.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()
	j2.Flush()
	outs2 := j2.Outputs()
	seen := map[int]bool{}
	exactlyOnce := len(outs2) == G*N && feedErrs.Load() == 0
	naiveOK := true
	for _, r := range outs2 {
		exactlyOnce = exactlyOnce && !seen[r.Seq]
		seen[r.Seq] = true
		wantV, wantF := "", false // naive AS OF over a0/a10/tombstone@20
		if r.TS >= 20 {
			wantV, wantF = "", false
		} else if r.TS >= 10 {
			wantV, wantF = "a10", true
		} else if r.TS >= 0 {
			wantV, wantF = "a0", true
		}
		naiveOK = naiveOK && r.Value == wantV && r.Found == wantF
	}
	ok("api concurrent feed exactly-once + flush==naive", exactlyOnce && naiveOK)

	if fails > 0 {
		os.Exit(1)
	}
}
