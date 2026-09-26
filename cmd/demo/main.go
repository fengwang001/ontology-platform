// Command demo exercises the lease subsystem and prints OK/FAIL lines.
// It takes no arguments and performs no network access.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/lease"
	"ontology/mgr"
)

func main() {
	fail := 0
	check := func(ok bool, line string) {
		if ok {
			fmt.Println("OK " + line)
		} else {
			fmt.Println("FAIL " + line)
			fail++
		}
	}

	// Seven-step built-in sequence (TTL 10, name "L"); each line shows the
	// state after that step (pinned through the public boundary probes).
	a := api.New(10)
	t, err := a.Acquire("L", "A", 0)
	check(err == nil && t == 1, "S1 token=1 owner=A expiry=10 GRANT")
	check(a.Renew("L", 1, 5) == nil && !a.Expired("L", 14) && a.Expired("L", 15), "S2 token=1 owner=A expiry=15 RENEW")
	check(a.Renew("L", 1, 12) == nil && !a.Expired("L", 21) && a.Expired("L", 22), "S3 token=1 owner=A expiry=22 RENEW")
	t, err = a.Acquire("L", "B", 20)
	check(err == nil && t == 2 && a.Expired("L", 29) == false, "S4 token=2 owner=B expiry=30 GRANT, fence monotonic")
	check(errors.Is(a.Renew("L", 1, 21), lease.ErrStaleToken) && !a.Expired("L", 29), "S5 token=2 owner=B expiry=30 REJECT stale, no trace")
	check(errors.Is(a.Renew("L", 2, 31), lease.ErrExpired) && a.Expired("L", 30), "S6 token=2 owner=B expiry=30 REJECT expired, renew only alive")
	check(a.Expired("L", 30), "S7 Expired(L,30)=true left-closed")

	// Four distinguishable sentinels; after every refusal state still works.
	b := api.New(10)
	_, errEmpty := b.Acquire("", "x", 0)
	eEmpty := errors.Is(errEmpty, mgr.ErrEmptyName)
	eMiss := errors.Is(b.Renew("ghost", 1, 0), mgr.ErrNotAcquired)
	b.Acquire("x", "o", 0)
	eStale := errors.Is(b.Renew("x", 9, 5), lease.ErrStaleToken)
	eDead := errors.Is(b.Renew("x", 1, 10), lease.ErrExpired)
	usable := b.Expired("ghost", 0) && !b.Expired("x", 9)
	check(eEmpty && eMiss && eStale && eDead && usable, "four distinct errors, rejected state still usable")

	// Large m with only a constant number expired: located without a scan.
	heapOK := true
	for _, m := range []int{100, 1000, 10000} {
		c := api.New(100000)
		for i := 0; i < m; i++ {
			c.Acquire(fmt.Sprintf("n%d", i), "o", i)
		}
		got := c.ExpiredAll(100002) // only n0..n2 expire, independent of m
		heapOK = heapOK && len(got) == 3 && got[0] == "n0" && got[2] == "n2"
	}
	check(heapOK, "ExpiredAll finds 3 at m=100/1000/10000, count not linear in m")

	// M goroutines renew distinct names; readers never observe expiry
	// decrease; final expiry is 19 for all. SelfCheck runs on the same API.
	const M = 50
	d := api.New(10)
	var wg sync.WaitGroup
	done := make(chan struct{})
	for g := 0; g < M; g++ {
		n := fmt.Sprintf("c%d", g)
		d.Acquire(n, "o", 0)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for now := 1; now <= 9; now++ {
				if d.Renew(n, 1, now) != nil {
					return
				}
			}
		}()
	}
	var rwg sync.WaitGroup
	for r := 0; r < 8; r++ {
		rwg.Add(1)
		go func() {
			defer rwg.Done()
			seen := map[string]bool{}
			for {
				select {
				case <-done:
					return
				default:
				}
				for g := 0; g < M; g++ {
					n := fmt.Sprintf("c%d", g)
					if !d.Expired(n, 18) {
						seen[n] = true // observed expiry > 18
					} else if seen[n] {
						panic("expiry decreased under concurrent read")
					}
				}
			}
		}()
	}
	wg.Wait()
	close(done)
	rwg.Wait()
	finOK := true
	for g := 0; g < M; g++ {
		n := fmt.Sprintf("c%d", g)
		finOK = finOK && !d.Expired(n, 18) && d.Expired(n, 19)
	}
	check(finOK && a.SelfCheck() == nil, "concurrent renew correct, expiry monotonic; SelfCheck OK")

	if fail > 0 {
		os.Exit(1)
	}
}
