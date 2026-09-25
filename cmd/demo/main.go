package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, verdict)
}

func main() {
	// 第三节八步序列：逐步核对 cwnd/state/partial
	s, _ := api.New(4, 1<<40)
	type row struct {
		cw, p int64
		st    api.State
	}
	steps := []row{{2, 0, api.SlowStart}, {3, 0, api.SlowStart}, {4, 0, api.SlowStart},
		{4, 1, api.CongAvoid}, {4, 2, api.CongAvoid}, {4, 3, api.CongAvoid}, {5, 0, api.CongAvoid}}
	ok := true
	for i, w := range steps {
		if s.OnAck() != nil || s.Cwnd() != w.cw || s.Partial() != w.p || s.State() != w.st {
			ok = false
			fmt.Printf("  step%d got (%d,%s,%d)\n", i+1, s.Cwnd(), s.State(), s.Partial())
		}
	}
	check("eight-step cwnd/state/partial per step", ok)
	check("step4 switch (cwnd=4,CongAvoid,partial=1)", steps[3] == row{4, 1, api.CongAvoid})
	check("step7 additive (cwnd=5,partial=0)", steps[6] == row{5, 0, api.CongAvoid})
	s.OnLoss()
	check("step8 halve (cwnd=1,ssthresh=2,SlowStart)", s.Cwnd() == 1 && s.Ssthresh() == 2 && s.State() == api.SlowStart && s.Partial() == 0)

	// 左闭切换：cwnd==ssthresh 即切 CongAvoid
	t, _ := api.New(2, 1<<40)
	t.OnAck() // cwnd=2==ssthresh，仍 SlowStart
	st := t.State()
	t.OnAck()
	check("left-closed switch at cwnd==ssthresh", st == api.SlowStart && t.State() == api.CongAvoid && t.Cwnd() == 2)

	// 三类可判定错误互不相同 + 被拒后状态不变
	_, e1 := api.New(1, 10)
	_, e2 := api.New(4, 0)
	u, _ := api.New(2, 3)
	u.OnAck()
	u.OnAck() // CongAvoid, partial=1
	cw, ss, p, st2 := u.Cwnd(), u.Ssthresh(), u.Partial(), u.State()
	e3 := u.OnAck() // cwnd 将触 maxCwnd=3 → 拒绝
	e4 := u.OnLoss()
	e5 := u.OnLoss()
	check("three distinct sentinel errors", errors.Is(e1, api.ErrBadParam) && errors.Is(e2, api.ErrBadParam) &&
		errors.Is(e3, api.ErrCwndLimit) && errors.Is(e5, api.ErrAlreadyFloor) && e4 == nil &&
		!errors.Is(api.ErrBadParam, api.ErrCwndLimit) && !errors.Is(api.ErrCwndLimit, api.ErrAlreadyFloor))
	check("rejected ops leave state unchanged", u.Cwnd() == 1 && u.Ssthresh() == ss && u.State() == api.SlowStart &&
		cw == 2 && p == 1 && st2 == api.CongAvoid && u.OnAck() == nil)

	// 加性增每次 OnAck 检查记录数 <= 1（只回报谓词，不暴露计数器数值）
	check("ack records checked per OnAck <= 1", u.VerifyAckCost())

	// 并发只读一致：N 个 goroutine 读同一实例，逐字段相同
	v, _ := api.New(4, 1<<40)
	for i := 0; i < 5; i++ {
		v.OnAck()
	}
	gcw, gss, gst := v.Cwnd(), v.Ssthresh(), v.State()
	start := make(chan struct{})
	var wg sync.WaitGroup
	bad := make(chan struct{}, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 200; j++ {
				if v.Cwnd() != gcw || v.Ssthresh() != gss || v.State() != gst {
					bad <- struct{}{}
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent read-only consistent", len(bad) == 0)

	check("SelfCheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}
