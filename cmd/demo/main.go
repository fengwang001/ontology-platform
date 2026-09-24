// Command demo exercises the asynchronous checkpoint barrier snapshot.
// It reads no arguments and performs no network access; exit code 0 iff all
// built-in judgments hold.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/align"
	"ontology/api"
)

var failed bool

func chk(name string, ok bool) {
	if ok {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
		failed = true
	}
}

func main() {
	c := align.NewChannel()
	c.Block(1)
	c.Hold(7)
	okAlign := c.Blocked() && c.BarrierID() == 1 && c.Pending()[0] == 7
	c.Reset()
	chk("align block/hold/reset", okAlign && !c.Blocked() && len(c.Pending()) == 0)

	evs := []api.Event{api.Rec(0, 5), api.Rec(1, 10), api.Bar(0, 1),
		api.Rec(0, 7), api.Rec(1, 3), api.Bar(1, 1), api.Rec(0, 2), api.Rec(1, 1)}
	want := []int{5, 15, 15, 15, 18, 25, 27, 28}
	o, _ := api.New(2)
	sums := make([]int, 8)
	step346 := true
	for i, ev := range evs {
		if err := o.Feed([]api.Event{ev}); err != nil {
			step346 = false
		}
		sums[i] = o.Sum()
		switch i {
		case 2: // step 3: ch0 blocked, no snapshot yet, sum unchanged
			_, has := o.Snapshot(1)
			step346 = step346 && !has && sums[i] == 15
		case 3: // step 4: +7 is buffered behind the barrier, sum stays 15
			step346 = step346 && sums[i] == 15
		case 5: // step 6: aligned snapshot 18 taken before draining +7 -> 25
			v, has := o.Snapshot(1)
			step346 = step346 && has && v == 18 && sums[i] == 25
		}
	}
	eq := true
	for i := range want {
		eq = eq && sums[i] == want[i]
	}
	fmt.Printf("OK  per-step sums %v snap[1]=18 final=%d\n", sums, o.Sum())
	chk("eight-event steps match NOTES table", eq && o.Sum() == 28)
	chk("step 3/4/6 block/buffer/snapshot judgments", step346)

	bad, _ := api.New(2)
	errs := []error{firstErr(api.New(0)), bad.Feed([]api.Event{api.Rec(-1, 1)}),
		bad.Feed([]api.Event{api.Bar(0, 0)}), bad.Feed([]api.Event{api.Bar(0, 1), api.Bar(0, 1)})}
	wantErr := []error{api.ErrChannels, api.ErrChannelRange, api.ErrBarrierID, api.ErrBarrierOrder}
	distinct := true
	for i, w := range wantErr {
		distinct = distinct && errors.Is(errs[i], w)
		for j := range wantErr {
			if j != i {
				distinct = distinct && !errors.Is(errs[i], wantErr[j])
			}
		}
	}
	chk("four distinct sentinel errors", distinct)

	before := o.Sum()
	rejectErr := o.Feed([]api.Event{api.Rec(0, 40), api.Bar(0, 0)})
	after, _ := o.Snapshot(1)
	chk("rejected batch leaves no trace", errors.Is(rejectErr, api.ErrBarrierID) && o.Sum() == before && after == 18)
	chk("SelfCheck incl. large-m O(1) alignment", api.SelfCheck() == nil)

	const N = 16
	var wg sync.WaitGroup
	consistent := true
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 1000; k++ {
				s, v := o.Sum(), 0
				v, _ = o.Snapshot(1)
				if s != 28 || v != 18 {
					consistent = false
				}
			}
		}()
	}
	wg.Wait()
	chk("concurrent readers all see sum=28 snap=18", consistent)

	if failed {
		os.Exit(1)
	}
}

func firstErr(_ any, err error) error { return err }
