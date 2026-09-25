package main

import (
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/arc"
	"ontology/off"
)

func ok(label string, good bool) bool {
	if good {
		fmt.Println("OK  ", label)
	} else {
		fmt.Println("FAIL", label)
	}
	return good
}

func main() {
	pass := true
	const ret int64 = 10

	// Section 3 eight-step sequence on one partition; record C and the
	// would-be Restart recovery after every step.
	s := off.New()
	wantC := []int64{100, 100, 110, 120, 120, 130, 130, 130}
	wantR := []int64{100, 100, 110, 120, 120, 130, 130, 100}
	steps := []func(){
		func() { _ = s.Commit(100, 0) },
		func() { _ = s.Checkpoint() },
		func() { _ = s.Commit(110, 10) },
		func() { _ = s.Commit(120, 15) },
		func() { _ = s.Evict(20, ret) },
		func() { _ = s.Commit(130, 30) },
		func() { _ = s.Evict(40, ret) },
		func() { _ = s.Evict(45, ret) },
	}
	gotC, gotR := make([]int64, 8), make([]int64, 8)
	for i, fn := range steps {
		fn()
		gotC[i], _ = s.Committed()
		gotR[i] = s.Recover()
	}
	pass = ok(fmt.Sprintf("8steps C=%v R=%v", gotC, gotR),
		reflect.DeepEqual(gotC, wantC) && reflect.DeepEqual(gotR, wantR)) && pass

	// (甲) step7=130 (cp-only wrongly gives 100); (乙) step8=100
	// (archive-only wrongly gives -inf).
	pass = ok("(甲)step7=130 not 100; (乙)step8=100 not -inf",
		gotR[6] == 130 && gotR[7] == 100 && off.NoCheckpoint < 100) && pass

	// (丙) strict boundary ts<cutoff keeps (110,ts=10) at Evict(20).
	g := off.New()
	_ = g.Commit(100, 0)
	_ = g.Checkpoint()
	_ = g.Commit(110, 10)
	_ = g.Evict(20, ret)
	pass = ok("(丙) strict< keeps (110,10)->recover 110 (<= would give 100)",
		g.Recover() == 110) && pass

	// Three distinct off sentinels (the fourth, invalid retention, is api).
	e1 := off.New()
	_ = e1.Commit(100, 0)
	nonMono := e1.Commit(100, 5) == off.ErrCommitNotMonotonic &&
		e1.Commit(110, -1) == off.ErrCommitNotMonotonic
	e2 := off.New()
	_ = e2.Commit(1, 0)
	_ = e2.Evict(20, ret)
	rewind := e2.Evict(19, ret) == off.ErrNowRewound
	noCommit := off.New().Checkpoint() == off.ErrNoCommit
	pass = ok("sentinels: commit-not-monotonic, now-rewound, no-commit",
		nonMono && rewind && noCommit) && pass

	// Rejected calls leave no trace: state unchanged and still usable.
	c, _ := e1.Committed()
	c2, _ := e2.Committed()
	stillUsable := e2.Evict(21, ret) == nil // rejected now=19 did not poison lastNow
	noTrace := c == 100 && e1.Recover() == 100 && c2 == 1 && stillUsable
	pass = ok("rejected ops leave no trace and instance still usable", noTrace) && pass

	// Large-m head-prefix eviction is observable black-box (the unexported
	// inspect counter is asserted only inside package arc).
	l := arc.New()
	const m, k = 10000, 250
	for i := 0; i < m; i++ {
		l.Append(int64(i+1), int64(i))
	}
	l.Evict(k)
	mx, _ := l.Max()
	pass = ok(fmt.Sprintf("large m=%d: evicted exactly k=%d, max=%d", m, k, mx),
		l.Len() == m-k && mx == m) && pass

	// api: 4th sentinel (invalid retention) and the built-in SelfCheck.
	_, bad := api.New(0)
	pass = ok("api invalid retention rejected; SelfCheck verifies invariants",
		bad == api.ErrInvalidRetention && api.ErrInvalidRetention != off.ErrCommitNotMonotonic) && pass
	{
		v, _ := api.New(10)
		pass = ok("SelfCheck passes", v.SelfCheck() == nil) && pass
	}

	// Concurrent readers see identical per-partition results (no sleeps).
	{
		v, _ := api.New(10)
		for p := 0; p < 4; p++ {
			_ = v.Commit(p, int64(100+p), 0)
			_ = v.Commit(p, int64(200+p), 5)
		}
		want := v.Restart()
		const N = 8
		var wg sync.WaitGroup
		var badN int32
		start := make(chan struct{})
		for g := 0; g < N; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := 0; i < 100; i++ {
					got := v.Restart()
					for p, w := range want {
						if c, ok := v.Committed(p); !ok || c != w || got[p] != w {
							atomic.StoreInt32(&badN, 1)
						}
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		pass = ok("concurrent readers agree per partition", atomic.LoadInt32(&badN) == 0) && pass
	}

	if !pass {
		fmt.Println("DEMO FAILED")
		return
	}
	fmt.Println("ALL OK")
}
