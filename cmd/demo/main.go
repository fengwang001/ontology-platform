package main

import (
	"errors"
	"fmt"
	"os"

	cdc "ontology/api"
)

func main() {
	bad := 0
	ok := func(c bool, m string) {
		if c {
			fmt.Println("OK: " + m)
		} else {
			bad++
			fmt.Println("FAIL: " + m)
		}
	}
	// 1. Eleven prescribed steps (steps 5, 7, 10 asserted explicitly).
	fails := map[cdc.Event]int{ev("A", 2): 1, ev("B", 1): 99, ev("B", 3): 1}
	want := []string{"A1E", "", "", "", "C1E", "", "A2EA3E", "", "A4E", "B1XB2E", "B3E"}
	type op struct {
		tick bool
		e    cdc.Event
	}
	ops := []op{
		{false, ev("A", 1)}, {false, ev("A", 2)}, {false, ev("B", 1)},
		{false, ev("A", 3)}, {false, ev("C", 1)}, {false, ev("B", 2)},
		{true, cdc.Event{}}, {false, ev("B", 3)}, {false, ev("A", 4)},
		{true, cdc.Event{}}, {true, cdc.Event{}},
	}
	a, _ := cdc.New(3, 8, fails)
	good := true
	for i, o := range ops {
		var ds []cdc.Decision
		if o.tick {
			ds = a.Tick()
		} else {
			ds, _ = a.Submit(o.e)
		}
		good = good && sig(ds) == want[i]
	}
	dl := a.DeadLetters()
	ok(good, "eleven-step decisions incl. steps 5/7/10")
	ok(len(dl) == 1 && dl[0] == ev("B", 1), "dead letters [B1]")
	// 2. Random interleaving equals the per-key serial reference.
	ok(randomMatch(7), "random stream equals per-key serial reference")
	// 3. Fault isolation.
	c, _ := cdc.New(3, 8, map[cdc.Event]int{ev("A", 1): 1})
	c.Submit(ev("A", 1))
	d3, _ := c.Submit(ev("C", 1))
	ok(sig(d3) == "C1E", "blocked key does not affect other keys")
	// 4. Three distinct sentinels and no-trace rejection.
	_, e0 := cdc.New(0, 1, nil)
	_, e1 := c.Submit(ev("A", 1))
	_, e2 := c.Submit(ev("", 9))
	z, _ := cdc.New(1, 0, map[cdc.Event]int{ev("Z", 1): 1})
	z.Submit(ev("Z", 1))
	_, e3 := z.Submit(ev("Z", 2))
	d4, _ := z.Submit(ev("Y", 1))
	ok(errors.Is(e0, cdc.ErrInvalid) && errors.Is(e1, cdc.ErrSeqOrder) &&
		errors.Is(e2, cdc.ErrInvalid) && errors.Is(e3, cdc.ErrBufferCap),
		"three distinguishable sentinel errors")
	ok(sig(d4) == "Y1E" && c.SelfCheck() == nil, "no trace after rejection; SelfCheck passes")
	// 5. Large m: one blocked key yields one decision regardless of idle keys.
	stable := true
	for _, m := range []int{100, 1000, 10000} {
		g, _ := cdc.New(3, m+8, map[cdc.Event]int{ev("Z", 1): 1})
		for i := 0; i < m; i++ {
			g.Submit(ev(fmt.Sprintf("k%05d", i), 1))
		}
		g.Submit(ev("Z", 1))
		stable = stable && sig(g.Tick()) == "Z1E"
	}
	ok(stable, "large-m tick ignores idle keys (counter pinned by white-box test)")
	// 6. Concurrent per-key producers + ticker preserve per-key serial order.
	ok(concurrentOK(), "concurrent submitters preserve per-key serial order")
	if bad != 0 {
		os.Exit(1)
	}
}
