package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sync"

	"ontology/aln"
	"ontology/api"
	"ontology/lww"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}

func main() {
	// aln: aligned time = min TS of accepted events per batch; strict lateness.
	var a aln.Aligner
	a.Begin()
	a.Accept(10)
	a.Accept(5)
	a.Close()
	a.Begin()
	a.Accept(20)
	a.Accept(12)
	a.Close()
	check("aln aligned [5 12], late 8 / on-time 12",
		slices.Equal(a.Times(), []int64{5, 12}) && a.Late(8) && !a.Late(12))

	// lww: max TS wins, tie goes to the later arrival; late events drop.
	var b aln.Aligner
	st := lww.New(&b)
	b.Begin()
	st.Feed("k", 10, 1)
	st.Feed("k", 5, 2)  // smaller TS loses
	st.Feed("k", 10, 3) // tie: later arrival wins
	b.Close()
	b.Begin()
	st.Feed("k", 4, 9) // late vs A(0)=5 -> dropped
	lv, _ := st.Value("k")
	check("lww max-TS + tie-late-arrival + drop", lv == 3 && st.Dropped() == 1)

	// api: the section-3 eight-event sequence, four batches.
	v, _ := api.New(8)
	evs := []api.Ev{
		{Key: "k", TS: 10, V: 1}, {Key: "k", TS: 5, V: 2},
		{Key: "k", TS: 20, V: 3}, {Key: "k", TS: 12, V: 4},
		{Key: "k", TS: 8, V: 5}, {Key: "k", TS: 12, V: 6},
		{Key: "k", TS: 25, V: 7}, {Key: "k", TS: 25, V: 9},
	}
	steps := make([]int, 0, 8)
	for i, e := range evs {
		if i%2 == 0 {
			v.BeginBatch()
		}
		if err := v.Feed(e); err != nil {
			check("section3 feed", false)
		}
		val, _ := v.Value("k")
		steps = append(steps, val)
		if i%2 == 1 {
			v.EndBatch()
		}
	}
	v.Flush()
	check("section3 steps [1 1 3 3 3 3 7 9]", slices.Equal(steps, []int{1, 1, 3, 3, 3, 3, 7, 9}))
	check("aligned [5 12 12 25]", slices.Equal(v.AlignedTimes(), []int64{5, 12, 12, 25}))
	check("dropped == 1", v.Dropped() == 1)

	// Four distinguishable sentinel errors; rejected ops leave no trace.
	w, _ := api.New(1)
	_, e0 := api.New(0)
	e1 := w.Feed(api.Ev{Key: "k", TS: 1})
	w.BeginBatch()
	e2 := w.Feed(api.Ev{TS: 1})
	okf := w.Feed(api.Ev{Key: "k", TS: 9, V: 1})
	e3 := w.Feed(api.Ev{Key: "k", TS: 10, V: 2})
	w.Flush()
	check("4 sentinel errors distinct",
		errors.Is(e0, api.ErrNonPositiveMax) && errors.Is(e1, api.ErrNoOpenBatch) &&
			errors.Is(e2, api.ErrEmptyKey) && errors.Is(e3, api.ErrBatchFull) &&
			!errors.Is(e0, api.ErrEmptyKey) && !errors.Is(e1, api.ErrBatchFull))
	wv, _ := w.Value("k")
	check("reject leaves no trace",
		okf == nil && wv == 1 && w.Dropped() == 0 && slices.Equal(w.AlignedTimes(), []int64{9}))

	// cmp == m (linear, not quadratic): pinned by aln's white-box test.
	err := exec.Command("go", "test", "-run", "TestCmpLinear", "-count=1", "./aln").Run()
	check("cmp == m for m in {100,1000,10000}", err == nil)

	check("concurrent read-only consistent", concOK(v))
	check("SelfCheck", v.SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}

// concOK: N goroutines read the same filled instance concurrently; every
// read must observe field-identical results. No sleeps.
func concOK(v api.V) bool {
	want, _ := v.Value("k")
	al, dr := v.AlignedTimes(), v.Dropped()
	var wg sync.WaitGroup
	start := make(chan struct{})
	bad := make(chan struct{}, 1)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 100; i++ {
				got, _ := v.Value("k")
				if got != want || !slices.Equal(v.AlignedTimes(), al) || v.Dropped() != dr || v.SelfCheck() != nil {
					select {
					case bad <- struct{}{}:
					default:
					}
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	select {
	case <-bad:
		return false
	default:
		return true
	}
}
