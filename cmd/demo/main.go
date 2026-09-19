// Command demo verifies each semantic of the idem executor and prints
// one OK/FAIL line per semantic.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"ontology/idem"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var fails int

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		fails++
	}
	fmt.Printf("%-28s %s\n", name, verdict)
}

func main() {
	clk := &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	e := idem.New(clk.now, time.Minute)
	calls := 0
	fn := func() (idem.Result, error) { calls++; return idem.Result{Code: 200, Body: "ok"}, nil }

	r1, o1, _ := e.Do("k1", "fp", fn)
	r2, o2, _ := e.Do("k1", "fp", fn)
	check("1 replay same key+fp", o1 == idem.Executed && o2 == idem.Replayed && r1 == r2 && calls == 1)

	ran := false
	_, _, err := e.Do("k1", "other", func() (idem.Result, error) { ran = true; return idem.Result{}, nil })
	r3, o3, _ := e.Do("k1", "fp", fn)
	check("2 fingerprint mismatch", errors.Is(err, idem.ErrFingerprintMismatch) && !ran && o3 == idem.Replayed && r3 == r1)

	biz := errors.New("biz")
	be := idem.New(clk.now, time.Minute)
	bn := 0
	bfn := func() (idem.Result, error) { bn++; return idem.Result{Code: 422}, biz }
	_, _, berr1 := be.Do("k", "fp", bfn)
	_, bo, berr2 := be.Do("k", "fp", bfn)
	check("3 business error cached", errors.Is(berr1, biz) && errors.Is(berr2, biz) && bo == idem.Replayed && bn == 1)

	re := idem.New(clk.now, time.Minute)
	rn := 0
	rfn := func() (idem.Result, error) {
		rn++
		if rn == 1 {
			return idem.Result{}, idem.Retriable(errors.New("db down"))
		}
		return idem.Result{Code: 200}, nil
	}
	re.Do("k", "fp", rfn)
	_, ro, rerr := re.Do("k", "fp", rfn)
	check("3b retriable not cached", rerr == nil && ro == idem.Executed && rn == 2)

	pe := idem.New(clk.now, time.Minute)
	pn := 0
	pfn := func() (idem.Result, error) {
		pn++
		if pn == 1 {
			panic("boom")
		}
		return idem.Result{Code: 200}, nil
	}
	_, _, perr := pe.Do("k", "fp", pfn)
	_, po, perr2 := pe.Do("k", "fp", pfn)
	check("4 panic releases slot", perr != nil && perr2 == nil && po == idem.Executed && pn == 2)

	se := idem.New(clk.now, time.Minute)
	var sc atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	outs := make([]idem.Outcome, 8)
	for i := range outs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, outs[i], _ = se.Do("k", "fp", func() (idem.Result, error) {
				sc.Add(1)
				<-release
				return idem.Result{Code: 200}, nil
			})
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	waited := 0
	for _, o := range outs {
		if o == idem.Waited {
			waited++
		}
	}
	check("5 singleflight waits", sc.Load() == 1 && waited == len(outs)-1)

	ne := idem.New(clk.now, time.Minute)
	slow := make(chan struct{})
	go ne.Do("slow", "fp", func() (idem.Result, error) { <-slow; return idem.Result{}, nil })
	time.Sleep(20 * time.Millisecond)
	fast := make(chan idem.Outcome, 1)
	go func() {
		_, o, _ := ne.Do("fast", "fp", func() (idem.Result, error) { return idem.Result{}, nil })
		fast <- o
	}()
	ok := <-fast == idem.Executed
	close(slow)
	check("6 keys don't block", ok)

	te := idem.New(clk.now, time.Minute)
	tn := 0
	tfn := func() (idem.Result, error) { tn++; return idem.Result{Code: tn}, nil }
	te.Do("k", "fp", tfn)
	clk.add(time.Minute - time.Nanosecond)
	tr1, to1, _ := te.Do("k", "fp", tfn)
	clk.add(time.Nanosecond)
	tr2, to2, _ := te.Do("k", "fp", tfn)
	check("7 ttl via injected clock", to1 == idem.Replayed && tr1.Code == 1 && to2 == idem.Executed && tr2.Code == 2)

	ie := idem.New(clk.now, time.Second)
	var ic atomic.Int32
	irel := make(chan struct{})
	go ie.Do("k", "fp", func() (idem.Result, error) { ic.Add(1); <-irel; return idem.Result{}, nil })
	time.Sleep(20 * time.Millisecond)
	clk.add(time.Hour)
	iout := make(chan idem.Outcome, 1)
	go func() {
		_, o, _ := ie.Do("k", "fp", func() (idem.Result, error) { ic.Add(1); return idem.Result{}, nil })
		iout <- o
	}()
	time.Sleep(20 * time.Millisecond)
	pending := ic.Load() == 1
	close(irel)
	check("8 in-flight never expires", pending && <-iout == idem.Waited && ic.Load() == 1)

	if fails > 0 {
		fmt.Printf("%d semantic(s) FAILED\n", fails)
		os.Exit(1)
	}
	fmt.Println("all semantics OK")
}
