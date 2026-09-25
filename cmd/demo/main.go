// Command demo exercises the hazard-pointer reclaimer; no args, no network.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"ontology/api"
	"ontology/hazard"
)

func rep(name, detail string, ok bool) bool {
	fmt.Printf("%s %-10s %s\n", map[bool]string{true: "OK  ", false: "FAIL"}[ok], name, detail)
	return ok
}
func sixStep() bool {
	r := api.New()
	ns := []string{"1P", "2R10", "3R20", "4C", "5U", "6C"}
	fs := []func() []int{
		func() []int { r.Protect(1, 10); return nil },
		func() []int { r.Retire(10); return nil },
		func() []int { r.Retire(20); return nil },
		r.Reclaim,
		func() []int { r.Unprotect(1); return nil },
		r.Reclaim,
	}
	var b string
	var fr [][]int
	for i, f := range fs {
		x := f()
		if x != nil {
			fr = append(fr, x)
		}
		b += fmt.Sprintf("%s%v %v | ", ns[i], x, r.Snapshot())
	}
	s := r.Snapshot()
	ok := len(fr) == 2 && fr[0][0] == 20 && fr[1][0] == 10 &&
		len(s.Hazards) == 0 && len(s.Retired) == 0
	return rep("six-step", b, ok)
}
func scenes() bool {
	bad := api.New()
	bad.Retire(10)
	uaf := len(bad.Reclaim()) == 1 // 10 gone before the late Protect: the deref is UAF
	good := api.New()
	good.Protect(1, 10)
	good.Retire(10)
	uaf = uaf && len(good.Reclaim()) == 0
	r := api.New()
	r.Protect(1, 10)
	r.Retire(10)
	r.Retire(20)
	f := r.Reclaim()
	blind := len(f) == 1 && f[0] == 20
	q := api.New()
	q.Protect(1, 10) // first publish, then another thread moves head 10 -> 20
	head := 20
	q.Unprotect(1)
	q.Protect(1, head) // reread after publish, mismatch -> retry on 20
	stale := q.Snapshot().Hazards[1] == 20
	rep("uaf-order", "deref-before-Protect UAFs 10; Protect-first retains it", uaf)
	rep("blind-free", fmt.Sprintf("safe frees %v; ignoring hazards frees protected 10", f), blind)
	rep("stale-head", "no-reread derefs unlinked 10; reread+retry gets 20", stale)
	return uaf && blind && stale
}
func errorsCheck() bool {
	r := api.New()
	r.Retire(2)
	r.Protect(3, 4)
	got := []error{r.Protect(1, 0), r.Retire(0), r.Retire(2), r.Unprotect(9), r.Protect(3, 5)}
	want := []error{api.ErrInvalidNode, api.ErrInvalidNode, api.ErrAlreadyRetired,
		api.ErrNotProtected, api.ErrAlreadyHeld}
	distinct := true
	for i := range got {
		if !errors.Is(got[i], want[i]) {
			distinct = false
		}
	}
	q := api.New()
	q.Retire(9)
	q.Protect(2, 3)
	before := fmt.Sprint(q.Snapshot())
	q.Protect(1, 0)
	q.Retire(0)
	q.Retire(9)
	q.Unprotect(7)
	q.Protect(2, 4)
	trace := fmt.Sprint(q.Snapshot()) != before
	q.Protect(5, 6)
	q.Retire(8)
	notrace := !trace && len(q.Reclaim()) == 2
	rep("sentinels", "four distinct decidable errors", distinct)
	rep("no-trace", "rejects leave zero trace; reuse afterwards works", notrace)
	return distinct && notrace
}
func o1Check() bool {
	return rep("o1-lookup", "per-node checks constant for m=100..10000",
		hazard.New().CheckConstantLookup(100, 1000, 10000) == nil)
}
func concurrent() bool {
	const n, rounds = 8, 100
	r := api.New()
	var held, cnt [n]atomic.Int32
	start, done := make(chan struct{}), make(chan struct{}, n)
	hold := func(w int) { r.Protect(w, w+1); held[w].Store(1); held[w].Store(0); r.Unprotect(w) }
	go func() {
		<-start
		for w := 0; w < n; w++ {
			go func(w int) {
				for k := 0; k < rounds; k++ {
					hold(w)
				}
				done <- struct{}{}
			}(w)
		}
	}()
	for i := 1; i <= n; i++ {
		r.Retire(i)
	}
	close(start)
	safe := true
	for left := n; left > 0; {
		for _, f := range r.Reclaim() {
			if held[f-1].Load() != 0 {
				safe = false
			}
			cnt[f-1].Add(1)
			left--
		}
	}
	for w := 0; w < n; w++ {
		<-done
	}
	once := true
	for i := range cnt {
		if cnt[i].Load() != 1 {
			once = false
		}
	}
	return rep("concurrent", fmt.Sprintf("%d nodes each reclaimed exactly once, safely", n), safe && once)
}
func main() {
	for _, c := range []func() bool{sixStep, scenes, errorsCheck, o1Check, concurrent} {
		if !c() {
			os.Exit(1)
		}
	}
}
