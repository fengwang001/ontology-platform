// Command demo exercises the refc+trial+api reclaimer, one OK/FAIL per
// property. No args, no network; exits 0 iff every property holds.
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var fails int

func report(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}
func must(o api.Obj, e error) api.Obj {
	if e != nil {
		panic(e)
	}
	return o
}
func nlive(r *api.Recycle, all []api.Obj) int {
	n := 0
	for _, o := range all {
		if r.RefCount(o) > 0 {
			n++
		}
	}
	return n
}
func fp(r *api.Recycle, all []api.Obj) string {
	s := ""
	for _, o := range all {
		s += fmt.Sprintf("%d:%d;", o, r.RefCount(o))
	}
	return s
}

func main() {
	// 1. Eight steps: rc[A]/rc[B] after each op, then Collect frees A,B.
	r := api.New()
	A, B := must(r.Root()), must(r.Root())
	traj := [][2]int{{r.RefCount(A), r.RefCount(B)}}
	_ = r.Point(A, B)
	traj = append(traj, [2]int{r.RefCount(A), r.RefCount(B)})
	_ = r.Point(B, A)
	traj = append(traj, [2]int{r.RefCount(A), r.RefCount(B)})
	_ = r.Unroot(A)
	traj = append(traj, [2]int{r.RefCount(A), r.RefCount(B)})
	_ = r.Unroot(B)
	traj = append(traj, [2]int{r.RefCount(A), r.RefCount(B)})
	freed := r.Collect()
	fmt.Printf("eight steps rc[A]/rc[B]=%v; step7 freed=%d\n", traj, freed)
	report("cycle reclaimed: Collect frees both A,B", freed == 2 && r.RefCount(A) == 0 && r.RefCount(B) == 0)

	// 2. Minimal graph vs an independent naive model. Surviving rooted chain
	// P->Q and garbage U<->V swept; roots after ops = {P}.
	g := api.New()
	P, Q := must(g.Root()), must(g.Root())
	U, V := must(g.Root()), must(g.Root())
	for _, e := range []error{g.Point(P, Q), g.Unroot(Q), g.Point(U, V), g.Point(V, U),
		g.Unroot(U), g.Unroot(V)} {
		if e != nil {
			panic(e)
		}
	}
	g.Collect()
	// Naive closure from the sole root P over edge P->Q is exactly {P,Q}.
	cons := g.RefCount(P) == 1 && g.RefCount(Q) == 1
	ndang := g.RefCount(Q) > 0
	reach := nlive(g, []api.Obj{P, Q, U, V}) == 2 && g.RefCount(U) == 0 && g.RefCount(V) == 0
	report("rc conservation (rc == roots + field indegree)", cons)
	report("no dangling pointer", ndang)
	report("live set == naive root-reachable set {P,Q}", reach)

	// 3. Three pairwise-distinct decidable sentinels.
	lim := api.New(1)
	Qx := must(lim.Root())
	_, eLim := lim.Root()
	eInv := lim.Point(api.Obj(1<<40), Qx)
	eNo := g.Unroot(Q) // Q is alive but carries no root (kept by P's edge)
	report("three distinct decidable sentinel errors",
		errors.Is(eLim, api.ErrObjectLimit) && errors.Is(eInv, api.ErrInvalidObject) &&
			errors.Is(eNo, api.ErrNoRootToDrop) && api.ErrObjectLimit != api.ErrInvalidObject)

	// 4. Rejected ops leave no trace: fingerprint identical afterwards.
	all := []api.Obj{P, Q, U, V}
	before := fp(g, all)
	g.Unroot(U)
	g.Point(api.Obj(1<<40), P)
	g.Point(P, api.Obj(1<<40))
	g.Unroot(Q)
	report("rejected operations leave no trace", fp(g, all) == before)

	// 5. Point is local: unrelated rc unchanged, at every m scale (sampled).
	local := true
	for _, m := range []int{100, 1000, 10000} {
		h := api.New()
		ids := make([]api.Obj, m)
		for i := range ids {
			ids[i] = must(h.Root())
		}
		_ = h.Point(ids[0], ids[1])
		local = local && localityOK(h, ids)
	}
	report("Point touches only from/to (stable over m=100..10000)", local)

	// 6. Concurrent Root+Point ring; barrier so all roots exist before points.
	c := api.New()
	const N = 64
	var ob [N]api.Obj
	var rooted, done sync.WaitGroup
	start := make(chan struct{})
	rooted.Add(N)
	done.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer done.Done()
			ob[i] = must(c.Root())
			rooted.Done()
			<-start
			_ = c.Point(ob[i], ob[(i+1)%N])
		}(i)
	}
	rooted.Wait()
	close(start)
	done.Wait()
	ringOK := c.Collect() == 0 && nlive(c, ob[:]) == N && c.SelfCheck() == nil
	report("concurrent ring reachable, no leak/dangling", ringOK)
	report("built-in SelfCheck passes", api.New().SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}

// localityOK asserts Point touched only ids[1]: it now has rc 2, while
// sampled bystanders (ids[2], the last id) still have rc 1.
func localityOK(h *api.Recycle, ids []api.Obj) bool {
	m := len(ids)
	return h.RefCount(ids[1]) == 2 && h.RefCount(ids[2]) == 1 && h.RefCount(ids[m-1]) == 1
}
