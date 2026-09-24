package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/tidx"
	"ontology/tlog"
)

var failed bool

func report(name string, ok bool) {
	s := "OK"
	if !ok {
		s = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, s)
}

func naive(base int64, ts []int64, t int64) (int64, bool) {
	for i, v := range ts {
		if v >= t {
			return base + int64(i), true
		}
	}
	return base + int64(len(ts)), false
}

func main() {
	lg := tlog.New(100, 1000)
	lg.Append([]int64{50, 40, 70, 60, 70, 65, 90, 80, 90, 85})
	es := lg.Index()
	idxOK := len(es) == 3 && es[0].TS == 50 && es[0].Off == 100 &&
		es[1].TS == 70 && es[1].Off == 102 && es[2].TS == 90 && es[2].Off == 106
	report(fmt.Sprintf("index (50,100)(70,102)(90,106) got %v", es), idxOK)

	a, _ := api.New(100, 1000)
	a.Append([]int64{50, 40, 70, 60, 70, 65, 90, 80, 90, 85})
	lk := func(t int64) (int64, bool) { return a.Lookup(t) }
	o45, f45 := lk(45)
	o65, f65 := lk(65)
	o80, f80 := lk(80)
	o85, f85 := lk(85)
	o91, f91 := lk(91)
	report("Lookup 45/65/80/85/91", o45 == 100 && f45 && o65 == 102 && f65 &&
		o80 == 106 && f80 && o85 == 106 && f85 && o91 == 110 && !f91)

	seq := make([]int64, 500) // 确定性非单调序列
	x := int64(7)
	for i := range seq {
		x = (x*6364136223846793005 + 1442695040888963407) >> 16
		if x < 0 {
			x = -x
		}
		seq[i] = x % 61
	}
	b, _ := api.New(0, 1000)
	b.Append(seq)
	naiveOK, monoOK := true, true
	prev := int64(-1)
	for t := int64(-1); t <= 62; t++ {
		go1, gf1 := b.Lookup(t)
		wo, wf := naive(0, seq, t)
		if go1 != wo || gf1 != wf {
			naiveOK = false
		}
		if prev >= 0 && go1 < prev {
			monoOK = false
		}
		prev = go1
	}
	report("naive consistency (non-monotonic)", naiveOK)
	report("query monotonic", monoOK)

	_, e1 := api.New(-1, 10)
	_, e2 := api.New(0, 0)
	c, _ := api.New(0, 2)
	c.Append([]int64{1})
	_, e3 := c.Append([]int64{-5})
	_, e4 := c.Append([]int64{1, 2})
	distinct := errors.Is(e1, api.ErrBadBase) && errors.Is(e2, api.ErrBadMaxMsgs) &&
		errors.Is(e3, api.ErrNegativeTS) && errors.Is(e4, api.ErrCapacity) &&
		!errors.Is(e1, e2) && !errors.Is(e3, e4) && !errors.Is(e2, e3)
	report("three decidable errors distinct", distinct)

	leoBefore := c.LEO()
	c.Append([]int64{-9})
	c.Append([]int64{5, 6})
	atomicOK := c.LEO() == leoBefore
	if _, err := c.Append([]int64{8}); err != nil || c.LEO() != leoBefore+1 {
		atomicOK = false
	}
	report("rejected batch leaves state unchanged", atomicOK)

	var ix tidx.Index
	report("probe count logarithmic in m", ix.SelfCheck())

	d, _ := api.New(0, 100000)
	done := make(chan struct{})
	var wg sync.WaitGroup
	stable := true
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			first, seen := int64(0), false
			for {
				select {
				case <-done:
					return
				default:
				}
				off, found := d.Lookup(50)
				if found {
					if seen && off != first {
						stable = false
					}
					first, seen = off, true
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		d.Append([]int64{int64(i % 100)})
	}
	close(done)
	wg.Wait()
	report("concurrent lookups stable", stable)

	report("api.SelfCheck (4 invariants)", a.SelfCheck() && b.SelfCheck())

	if failed {
		os.Exit(1)
	}
}
