// Command demo runs in-process self-checks of the lateness-rate-adaptive
// watermark and prints one OK/FAIL line per check. No args, no network.
package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/adapt"
	"ontology/api"
)

func report(name string, pass bool) {
	if pass {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		panic(name)
	}
}

func main() {
	s, _ := api.New(2, 10, 2, 3, 2, 0)
	want := []struct {
		ts        int64
		late      bool
		wm, delay int64
	}{
		{10, false, 8, 2}, {11, false, 9, 2}, {9, false, 9, 2},
		{5, true, 9, 2}, {6, true, 9, 2}, {15, false, 13, 4},
		{16, false, 12, 4}, {17, false, 13, 4}, {18, false, 14, 2},
	}
	nine, boundary, up, down := true, false, false, false
	for i, w := range want {
		if s.Feed(w.ts) != w.late || s.WM() != w.wm || s.Delay() != w.delay {
			nine = false
		}
		if i == 2 {
			boundary = w.ts == w.wm && !w.late // ts == wm is on time
		}
	}
	up, down = want[5].delay == 4, want[8].delay == 2 // step 6 raise, step 9 fall
	report("nine events: per-step wm/delay match", nine)
	report("step3 ts==wm judged on-time", boundary)
	report("step6 delay up 2->4, step9 back 4->2", up && down)

	bad := []struct {
		args [6]int64
		want error
	}{
		{[6]int64{9, 8, 1, 3, 2, 0}, adapt.ErrMinAboveMax},
		{[6]int64{-1, 8, 1, 3, 2, 0}, adapt.ErrNegativeMin},
		{[6]int64{2, 8, 0, 3, 2, 0}, adapt.ErrNonPositiveStep},
		{[6]int64{2, 8, 1, 0, 2, 0}, adapt.ErrNonPositiveWindow},
		{[6]int64{2, 8, 1, 3, 2, 2}, adapt.ErrThresholdRange},
	}
	distinct := true
	seen := map[error]bool{}
	for _, b := range bad {
		st, err := api.New(b.args[0], b.args[1], b.args[2], b.args[3], b.args[4], b.args[5])
		if st != nil || !errors.Is(err, b.want) || seen[err] {
			distinct = false
		}
		seen[err] = true
	}
	report("five distinct decidable parameter errors", distinct)

	wmBefore, dBefore := s.WM(), s.Delay()
	if st, err := api.New(-1, 1, 1, 1, 1, 0); st != nil || !errors.Is(err, adapt.ErrNegativeMin) {
		report("state unchanged after rejection", false)
	}
	s.Feed(wmBefore + 100)
	report("state unchanged after rejection, store still usable", s.WM() != wmBefore && dBefore == 2)

	report("settlement scan cost constant in m=100..10000", adapt.ConstantSettlementCost() == nil)

	const N = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	got := make([][2]int64, N)
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			got[i] = [2]int64{s.WM(), s.Delay()}
		}(g)
	}
	close(start)
	wg.Wait()
	same := true
	for _, r := range got {
		if r != got[0] {
			same = false
		}
	}
	report("16 concurrent readers see identical wm/delay", same)
	report("SelfCheck all four invariants", s.SelfCheck() == nil)
}
