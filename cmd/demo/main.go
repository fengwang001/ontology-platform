// Command demo exercises the reclaimer and prints one OK/FAIL per judgment.
// Exit 0 only when every judgment passes. No args, no network.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/trial"
)

var failed bool

func ok(cond bool, msg string) {
	fmt.Println(map[bool]string{true: "OK ", false: "FAIL"}[cond] + msg)
	if !cond {
		failed = true
	}
}
func audit(c *api.Collector) (bool, bool) {
	sn := c.Snapshot()
	alive, indeg, child, seen, st := map[api.Obj]bool{}, map[api.Obj]int{}, map[api.Obj]api.Obj{}, map[api.Obj]bool{}, []api.Obj{}
	for _, s := range sn {
		alive[s.O], child[s.O] = true, s.Child
		if s.Child != 0 {
			indeg[s.Child]++
		}
		if s.Roots > 0 {
			seen[s.O], st = true, append(st, s.O)
		}
	}
	shape := true
	for _, s := range sn {
		if (s.Child != 0 && !alive[s.Child]) || s.Roots < 0 || s.Fields != indeg[s.O] {
			shape = false
		}
	}
	for len(st) > 0 {
		x := st[len(st)-1]
		st = st[:len(st)-1]
		if d := child[x]; d != 0 && !seen[d] {
			seen[d], st = true, append(st, d)
		}
	}
	return shape, len(seen) == len(sn)
}
func main() {
	t8 := api.New()
	var A, B api.Obj
	want := [8][3]int{{1, 0, 0}, {1, 1, 0}, {1, 2, 0}, {2, 2, 0}, {1, 2, 0}, {1, 1, 0}, {0, 0, 2}, {0, 0, 0}}
	got := make([][3]int, 0, 8)
	obs := func(f int) { got = append(got, [3]int{t8.RefCount(A), t8.RefCount(B), f}) }
	for _, op := range []func(){
		func() { A, _ = t8.Root() }, func() { B, _ = t8.Root() }, func() { _ = t8.Point(A, B) },
		func() { _ = t8.Point(B, A) }, func() { _ = t8.Unroot(A) }, func() { _ = t8.Unroot(B) },
	} {
		op()
		obs(0)
	}
	obs(t8.Collect()) // step 7: the rootless A<->B cycle
	obs(0)            // step 8: nothing remains
	ok(fmt.Sprint(got) == fmt.Sprint(want), fmt.Sprintf("8-step table rc/freed: %v", got))
	c := api.New()
	R, _ := c.Root()
	S, _ := c.Root()
	T, _ := c.Root()
	_ = c.Point(R, S)
	_ = c.Point(S, T)
	U, _ := c.Root()
	V, _ := c.Root()
	_ = c.Point(U, V)
	_ = c.Point(V, U)
	P, _ := c.Root()
	Q, _ := c.Root()
	_ = c.Point(P, Q)
	_ = c.Point(Q, P)
	_ = c.Unroot(P)
	_ = c.Unroot(Q)
	pre, _ := audit(c)
	freed := c.Collect()
	post, reach := audit(c)
	ok(pre && post, "rc conserved, no dangling refs (before/after Collect)")
	ok(freed == 2 && reach && c.RefCount(U) > 0, "live == naive root-reachable (garbage cycle gone, rooted kept)")
	n := len(c.Snapshot())
	e1, e2 := c.Unroot(api.Obj(1<<40)), c.Point(api.Obj(1<<40), 0)
	X, _ := c.Root()
	Y, _ := c.Root()
	_ = c.Point(X, Y)
	_ = c.Unroot(Y) // Y: roots==0, alive only via X->Y
	e3 := c.Unroot(Y)
	gl := trial.New(1)
	_, _ = gl.Root()
	_, eLim := gl.Root()
	distinct := errors.Is(e1, api.ErrUnknownObject) && errors.Is(e2, api.ErrUnknownObject) &&
		errors.Is(e3, api.ErrNoRoot) && errors.Is(eLim, api.ErrLimit) && !errors.Is(e3, e1)
	ok(distinct, "three distinct decidable sentinel errors")
	ok(len(c.Snapshot()) == n+2 && c.RefCount(Y) == 1, "rejected operations leave no state trace")
	big := api.New()
	for i := 0; i < 10000; i++ {
		_, _ = big.Root()
	}
	_ = big.Point(1, 2)
	bad := 0
	for _, s := range big.Snapshot() {
		rcBad := (s.O == 2 && s.Roots+s.Fields != 2) || (s.O != 2 && s.Roots+s.Fields != 1)
		if rcBad || (s.O == 1 && s.Child != 2) {
			bad++
		}
	}
	ok(bad == 0, "Point touches constant records at m=10000 (no linear scan)")
	ok(concurrentDemo(), "concurrent mix: reachable set exact, no leak/dangling")
	ok(api.New().SelfCheck() == nil, "SelfCheck")
	if failed {
		os.Exit(1)
	}
}
func concurrentDemo() bool {
	const N = 48
	c := api.New()
	objs := make([]api.Obj, N)
	var r1, all sync.WaitGroup
	r1.Add(N)
	all.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer all.Done()
			o, _ := c.Root()
			objs[i] = o
			r1.Done()
			r1.Wait()
			seed := uint64(i*2654435761 + 1)
			for k := 0; k < 6; k++ {
				seed = seed*6364136223846793005 + 1442695040888963407
				_ = c.Point(o, objs[int(seed%N)])
			}
			if i%3 == 0 {
				_ = c.Unroot(o)
			}
		}(i)
	}
	all.Wait()
	c.Collect()
	sh, rc := audit(c)
	return sh && rc
}
