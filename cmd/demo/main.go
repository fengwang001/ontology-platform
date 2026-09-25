// Command demo exercises the dynamically-registered phaser and prints OK/FAIL lines.
package main

import (
	"fmt"
	"os"
	"reflect"
	"sync/atomic"

	"ontology/api"
	"ontology/phase"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		failed = true
		fmt.Println("FAIL " + name)
	}
}

func main() {
	// 1. Section 3 four steps: phase and unarrived set after each step.
	p, _ := api.NewPhaser(2)
	p.Arrive(0)
	s0 := p.Snapshot()
	id, _, _ := p.Register()
	s1 := p.Snapshot()
	p.Arrive(1)
	s2 := p.Snapshot()
	cAt, _ := p.Arrive(id)
	s3 := p.Snapshot()
	got := [4]string{fmt.Sprintf("%d%v", s0.Phase, s0.Unarrived),
		fmt.Sprintf("%d%v", s1.Phase, s1.Unarrived),
		fmt.Sprintf("%d%v", s2.Phase, s2.Unarrived),
		fmt.Sprintf("%d%v", s3.Phase, s3.Unarrived)}
	want := [4]string{"0map[1:{}]", "0map[1:{} 2:{}]", "0map[2:{}]", "1map[0:{} 1:{} 2:{}]"}
	check("four steps phase+unarrived", got == want)

	// 2. Correct: C joins CURRENT phase (s2.Phase==0, cAt==0); join-next bug
	//    advances early to 1 and makes C arrive at phase 1.
	b3, b4, bc := joinNextBug()
	check("register current vs next phase", s2.Phase == 0 && cAt == 0 && b3 == 1 && b4 == 1 && bc == 1)

	// 3. Correct arrive-then-deregister ends at phase 1; the deregister-first
	//    bug over-advances to 2.
	q, _ := api.NewPhaser(2)
	q.Arrive(1)
	v, _ := q.ArriveAndDeregister(0)
	check("arrive-before-deregister=1 (bug=2)", v == 0 && q.Phase() == 1 && deregFirstBug() == 2)

	// 4. The last arriver returns the PRE-advance phase; both parties return 0.
	r, _ := api.NewPhaser(2)
	a, _ := r.Arrive(0)
	b, _ := r.Arrive(1)
	check("last Arrive returns pre-advance", a == 0 && b == 0 && r.Phase() == 1)

	// 5. Four decidable, pairwise-distinct sentinel errors.
	_, e0 := api.NewPhaser(0)
	z, _ := api.NewPhaser(2)
	_, e1 := z.Arrive(9)
	z.Arrive(0)
	_, e2 := z.Arrive(0)
	tp, _ := api.NewPhaser(1)
	tp.ArriveAndDeregister(0)
	_, _, e3 := tp.Register()
	distinct := e0 != e1 && e0 != e2 && e0 != e3 && e1 != e2 && e1 != e3 && e2 != e3
	check("four distinct decidable errors", distinct && e0 == api.ErrBadN && e3 == api.ErrTerminated)

	// 6. Rejected ops leave zero trace.
	y, _ := api.NewPhaser(2)
	y.Arrive(0)
	before := y.Snapshot()
	_, de := y.Arrive(0)
	_, ue := y.Arrive(7)
	check("rejected ops change nothing", de == api.ErrDuplicateArrival && ue == api.ErrUnknownParty &&
		reflect.DeepEqual(before, y.Snapshot()))

	// 7. O(1) advance probe stays 1 for m up to 10000 (value never exported).
	check("advance probe ==1 for m<=10000", phase.SelfCheck() == nil)

	// 8. N parties, K lockstep rounds without sleeps; all stop at same phase.
	const N, K = 8, 100
	cp, _ := api.NewPhaser(N)
	var perRound [K + 1]int64
	var done int64
	stop := make(chan struct{})
	for g := 0; g < N; g++ {
		go func(id int) {
			for rd := 0; rd < K; rd++ {
				cp.Arrive(id)
				if cp.AwaitAdvance(rd) == rd+1 {
					atomic.AddInt64(&perRound[rd+1], 1)
				}
			}
			if atomic.AddInt64(&done, 1) == N {
				close(stop)
			}
		}(g)
	}
	<-stop
	allSame := true
	for k := 1; k <= K; k++ {
		if atomic.LoadInt64(&perRound[k]) != N {
			allSame = false
		}
	}
	check("concurrent convergence; phase==K", allSame && cp.Phase() == K)

	if failed {
		os.Exit(1)
	}
}

// joinNextBug models Register joining the NEXT phase: B arriving empties phase
// 0 early (advance to 1); C then arrives at phase 1.
func joinNextBug() (int, int, int) {
	un := map[int]struct{}{0: {}, 1: {}}
	ph := 0
	delete(un, 0) // A arrives
	// C registered for the NEXT phase: absent from un.
	delete(un, 1) // B arrives: phase 0 empties prematurely
	ph++
	return ph, ph, ph // after step3=1, after step4=1, C arrives at 1
}

// deregFirstBug models deregister-before-arrive with a plain counter: the
// deregister drops the counter to 0 (advance), then the arrive half decrements
// again (second advance), ending at 2 instead of 1.
func deregFirstBug() int {
	parties, cnt, ph := 2, 2, 0
	cnt-- // B arrives
	parties--
	cnt-- // A deregistered while unarrived
	if cnt == 0 {
		ph++
		cnt = parties
	}
	cnt-- // arrive half runs without membership recheck
	if cnt == 0 {
		ph++
	}
	return ph
}
