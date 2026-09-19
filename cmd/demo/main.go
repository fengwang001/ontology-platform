// Command demo exercises each documented semantic of package idem and prints
// one OK/FAIL line per check.
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

var now = time.Unix(1_000_000, 0)

func clock() time.Time { return now }

func main() {
	failed := 0
	check := func(name string, ok bool) {
		verdict := "OK"
		if !ok {
			verdict = "FAIL"
			failed++
		}
		fmt.Printf("%-28s %s\n", name, verdict)
	}

	e := idem.New(clock, time.Minute)
	calls := 0
	fn := func() (idem.Result, error) { calls++; return idem.Result{Code: 200, Body: "r1"}, nil }
	r1, oc1, _ := e.Do("k1", "fp", fn)
	r2, oc2, _ := e.Do("k1", "fp", fn)
	check("1 replay same key+fp", oc1 == idem.Executed && oc2 == idem.Replayed && r1 == r2 && calls == 1)

	_, _, err := e.Do("k1", "other", fn)
	r3, oc3, _ := e.Do("k1", "fp", fn)
	check("2 fingerprint mismatch", errors.Is(err, idem.ErrFingerprintMismatch) && oc3 == idem.Replayed && r3 == r1 && calls == 1)

	biz := errors.New("business")
	e2 := idem.New(clock, time.Minute)
	bcalls := 0
	bfn := func() (idem.Result, error) { bcalls++; return idem.Result{Code: 422}, biz }
	_, _, be1 := e2.Do("k", "fp", bfn)
	_, boc, be2 := e2.Do("k", "fp", bfn)
	check("3a business error cached", errors.Is(be1, biz) && errors.Is(be2, biz) && boc == idem.Replayed && bcalls == 1)

	e3 := idem.New(clock, time.Minute)
	rcalls := 0
	rfn := func() (idem.Result, error) {
		rcalls++
		if rcalls == 1 {
			return idem.Result{}, idem.Retriable(errors.New("infra"))
		}
		return idem.Result{Code: 200}, nil
	}
	e3.Do("k", "fp", rfn)
	_, roc, _ := e3.Do("k", "fp", rfn)
	check("3b retriable not cached", roc == idem.Executed && rcalls == 2)

	e4 := idem.New(clock, time.Minute)
	pcalls := 0
	pfn := func() (idem.Result, error) {
		pcalls++
		if pcalls == 1 {
			panic("boom")
		}
		return idem.Result{Code: 200}, nil
	}
	_, _, perr := e4.Do("k", "fp", pfn)
	_, poc, _ := e4.Do("k", "fp", pfn)
	check("4 panic releases slot", perr != nil && poc == idem.Executed && pcalls == 2)

	e5 := idem.New(clock, time.Minute)
	var scalls atomic.Int32
	sfn := func() (idem.Result, error) {
		scalls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return idem.Result{Code: 201}, nil
	}
	var wg sync.WaitGroup
	var execs, waits atomic.Int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, oc, _ := e5.Do("k", "fp", sfn)
			if oc == idem.Executed {
				execs.Add(1)
			} else if oc == idem.Waited {
				waits.Add(1)
			}
		}()
	}
	wg.Wait()
	check("5 single-flight waits", scalls.Load() == 1 && execs.Load() == 1 && waits.Load() == 7)

	e6 := idem.New(clock, time.Minute)
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		e6.Do("slow", "fp", func() (idem.Result, error) {
			close(started)
			<-release
			return idem.Result{}, nil
		})
	}()
	<-started
	fast := make(chan idem.Outcome, 1)
	go func() { _, oc, _ := e6.Do("fast", "fp", sfn); fast <- oc }()
	free := false
	select {
	case <-fast:
		free = true
	case <-time.After(time.Second):
	}
	close(release)
	check("6 keys do not block", free)

	now = time.Unix(1_000_000, 0)
	e7 := idem.New(clock, 10*time.Second)
	tcalls := 0
	tfn := func() (idem.Result, error) { tcalls++; return idem.Result{Code: tcalls}, nil }
	e7.Do("k", "fp", tfn)
	now = now.Add(9 * time.Second)
	_, toc1, _ := e7.Do("k", "fp", tfn)
	now = now.Add(time.Second)
	tr, toc2, _ := e7.Do("k", "fp", tfn)
	check("7 ttl injected clock", toc1 == idem.Replayed && toc2 == idem.Executed && tr.Code == 2)

	now = time.Unix(1_000_000, 0)
	e8 := idem.New(clock, time.Second)
	release2 := make(chan struct{})
	started2 := make(chan struct{})
	var icalls atomic.Int32
	go func() {
		e8.Do("k", "fp", func() (idem.Result, error) {
			icalls.Add(1)
			close(started2)
			<-release2
			return idem.Result{Code: 200}, nil
		})
	}()
	<-started2
	now = now.Add(time.Hour)
	waited := make(chan idem.Outcome, 1)
	go func() { _, oc, _ := e8.Do("k", "fp", tfn); waited <- oc }()
	blocked := false
	select {
	case <-waited:
	case <-time.After(20 * time.Millisecond):
		blocked = true
	}
	close(release2)
	woc := <-waited
	check("8 in-flight never expires", blocked && woc == idem.Waited && icalls.Load() == 1)

	if failed > 0 {
		fmt.Printf("%d check(s) failed\n", failed)
		os.Exit(1)
	}
	fmt.Println("all 8 semantics OK")
}
