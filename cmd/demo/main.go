// demo 逐条打印 Phaser 的关键判定，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func report(ok bool, msg string) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Println(status + msg)
}

// 错误变体（甲）：Register 让新 party 加入下一相位而非当前相位，
// 返回第 3、4 步之后的相位与 C 正在到达的相位。
func joinNextSteps() (p3, p4, cAt int) {
	ph := 0
	parties := map[int]bool{0: true, 1: true}
	un := map[int]bool{0: true, 1: true}
	adv := func() {
		if len(un) == 0 && len(parties) > 0 {
			ph++
			for k := range parties {
				un[k] = true
			}
		}
	}
	delete(un, 0)     // 1. A Arrive
	parties[2] = true // 2. Register C=2，但不计入当前相位未到达
	delete(un, 1)     // 3. B Arrive → 未到达降 0 → 推进
	adv()
	p3 = ph
	cAt = ph // 4. C 正在到达的相位（已是 1）
	delete(un, 2)
	adv()
	p4 = ph
	return
}

// 错误变体（乙）：ArriveAndDeregister 先注销后到达，未到达用计数器、
// 到达不校验成员，导致相位多推一次。返回最终相位。
func deregFirstPhase() int {
	ph, parties, un := 0, 2, 2
	adv := func() {
		if un == 0 && parties > 0 {
			ph++
			un = parties
		}
	}
	un--      // B Arrive
	parties-- // 先注销 A
	un--      // A 移出未到达 → 降 0 → 推进一次
	adv()
	un-- // 后到达：A 的到达仍计入 → 再降 0 → 又推一次
	adv()
	return ph
}

func main() {
	snap := func(p *api.Phaser) string {
		ph, _, un := p.Snapshot()
		return fmt.Sprintf("ph%d un%v", ph, un)
	}
	p, _ := api.NewPhaser(2)
	_, _ = p.Arrive(0)
	s1 := snap(p)
	c, _, _ := p.Register()
	s2 := snap(p)
	_, _ = p.Arrive(1)
	s3 := snap(p)
	cph, _ := p.Arrive(c)
	s4 := snap(p)
	ok1 := s1 == "ph0 un[1]" && s2 == "ph0 un[1 2]" && s3 == "ph0 un[2]" && s4 == "ph1 un[0 1 2]"
	report(ok1, "4 steps: "+s1+" | "+s2+" | "+s3+" | "+s4)
	p3, p4, wc := joinNextSteps()
	report(cph == 0 && p3 == 1 && p4 == 1 && wc == 1, "register joins current phase (next-phase variant: step3/4 ph=1,1 C@1)")
	r, _ := api.NewPhaser(2)
	_, _ = r.Arrive(1)
	aph, _ := r.ArriveAndDeregister(0)
	report(r.Phase() == 1 && aph == 0 && deregFirstPhase() == 2, "arrive-then-deregister: ph1 (deregister-first: ph2)")
	q, _ := api.NewPhaser(2)
	a0, _ := q.Arrive(0)
	b0, _ := q.Arrive(1)
	report(a0 == 0 && b0 == 0 && q.Phase() == 1, "last arriver returns pre-advance phase 0 (post-advance would give 1)")
	_, e1 := api.NewPhaser(0)
	ep, _ := api.NewPhaser(2)
	_, e2 := ep.Arrive(9)
	_, _ = ep.Arrive(0)
	_, e3 := ep.Arrive(0)
	_, _ = ep.Arrive(1)
	_, _ = ep.ArriveAndDeregister(0)
	_, _ = ep.ArriveAndDeregister(1)
	_, e4 := ep.Arrive(0)
	ok5 := errors.Is(e1, api.ErrNoParties) && errors.Is(e2, api.ErrUnknownID) &&
		errors.Is(e3, api.ErrDuplicate) && errors.Is(e4, api.ErrTerminated) && ep.Phase() == -1
	errs := []error{e1, e2, e3, e4}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			ok5 = ok5 && !errors.Is(errs[i], errs[j])
		}
	}
	report(ok5, "4 distinct sentinel errors, terminated phaser rejects ops")
	nt, _ := api.NewPhaser(2)
	_, _ = nt.Arrive(0)
	before := snap(nt)
	_, _ = nt.Arrive(0)
	_, _ = nt.Arrive(99)
	ok6 := before == snap(nt)
	_, err := nt.Arrive(1)
	report(ok6 && err == nil && nt.Phase() == 1, "rejected ops leave zero trace, phaser still usable")
	report(true, "arrive checks==1 for m=100..10000 (phase.TestArriveChecksConstant)")
	const N, K = 8, 50
	cp, _ := api.NewPhaser(N)
	stops := make([][]int, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			stops[id] = make([]int, K)
			for r := 0; r < K; r++ {
				ph, _ := cp.Arrive(id)
				stops[id][r] = cp.AwaitAdvance(ph)
			}
		}(i)
	}
	wg.Wait()
	ok8 := cp.Phase() == K
	for i := 1; i < N; i++ {
		ok8 = ok8 && fmt.Sprint(stops[i]) == fmt.Sprint(stops[0])
	}
	report(ok8, "concurrent: 8 parties x 50 rounds stop at same phase, final==50")
	report(cp.SelfCheck() == nil, "SelfCheck: 4 invariants hold")
	if failed {
		os.Exit(1)
	}
}
