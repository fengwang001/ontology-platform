package main

import (
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/conn"
)

var fails int

func check(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	// 1. 第三节七步序列：状态、动作、enterTime（2MSL=100）
	c := api.New(50)
	type want struct {
		state    string
		enter    int64
		fin, ack int
	}
	steps := []struct {
		run func() error
		w   want
	}{
		{func() error { return c.Close() }, want{"FIN_WAIT_1", 0, 1, 0}},
		{func() error { return c.RecvACK(0) }, want{"FIN_WAIT_2", 0, 1, 0}},
		{func() error { return c.RecvData() }, want{"FIN_WAIT_2", 0, 1, 0}},
		{func() error { return c.RecvFIN(100) }, want{"TIME_WAIT", 100, 1, 1}},
		{func() error { return c.RecvFIN(150) }, want{"TIME_WAIT", 150, 1, 2}},
		{func() error { return c.Tick(249) }, want{"TIME_WAIT", 150, 1, 2}},
		{func() error { return c.Tick(250) }, want{"CLOSED", 150, 1, 2}},
	}
	ok := true
	for i, s := range steps {
		if s.run() != nil || c.State() != s.w.state || c.EnterTime() != s.w.enter || c.SentFIN() != s.w.fin || c.SentACK() != s.w.ack {
			ok = false
			_ = i
		}
	}
	check("七步序列状态/动作/enterTime 与推导表一致", ok)

	// 2. 第 3 步半关闭：FIN_WAIT_2 与 CLOSE_WAIT 收数据合法、状态不变
	h := api.New(50)
	h.Close()
	h.RecvACK(0)
	w := api.New(50)
	w.RecvFIN(0)
	check("半关闭 FIN_WAIT_2/CLOSE_WAIT 收数据合法且状态不变",
		h.RecvData() == nil && h.State() == "FIN_WAIT_2" && w.RecvData() == nil && w.State() == "CLOSE_WAIT")

	// 3. TIME_WAIT 边界：199（<enter+2MSL）不走、200（=enter+2MSL）左闭走
	b := api.New(50)
	b.Close()
	b.RecvACK(0)
	b.RecvFIN(100)
	check("TIME_WAIT 边界 Tick(199) 保持、Tick(200) 左闭进 CLOSED",
		b.Tick(199) == nil && b.State() == "TIME_WAIT" && b.Tick(200) == nil && b.State() == "CLOSED")

	// 4. 同时关闭：双方 ESTABLISHED 同时 Close，各自 CLOSING→TIME_WAIT→CLOSED
	sa, sb := api.New(50), api.New(50)
	sa.Close()
	sb.Close()
	sa.RecvFIN(1)
	sb.RecvFIN(1)
	sa.RecvACK(2)
	sb.RecvACK(2)
	sa.Tick(102)
	sb.Tick(102)
	check("同时关闭双方 CLOSING→TIME_WAIT→CLOSED", sa.State() == "CLOSED" && sb.State() == "CLOSED")

	// 5. 非法事件零副作用（ESTABLISHED 下 RecvACK 非法）
	z := api.New(50)
	st, en, f, a := z.State(), z.EnterTime(), z.SentFIN(), z.SentACK()
	err := z.RecvACK(0)
	check("非法事件零副作用且可判定", err != nil && z.State() == st && z.EnterTime() == en && z.SentFIN() == f && z.SentACK() == a)

	// 6. 四类可判定错误互不相同
	e1 := api.New(50)
	ackErr := e1.RecvACK(0)
	e2 := api.New(50)
	e2.RecvFIN(0)
	finErr := e2.RecvFIN(1)
	e3 := api.New(50)
	e3.Close()
	e3.RecvFIN(0)
	dataErr := e3.RecvData()
	e4 := api.New(50)
	e4.RecvFIN(10)
	clkErr := e4.Tick(9)
	distinct := map[error]bool{ackErr: true, finErr: true, dataErr: true, clkErr: true}
	check("非法ACK/FIN/DATA/时钟回退四类错误可判定且互不相同",
		ackErr == conn.ErrIllegalACK && finErr == conn.ErrIllegalFIN && dataErr == conn.ErrIllegalData && clkErr == conn.ErrClockBack && len(distinct) == 4)

	// 7. 事件分派 O(1)：每次检查规则数恰好 1，m 档总数恰为 m
	o1 := true
	for _, m := range []int{100, 777, 10000} {
		o1 = o1 && conn.VerifyO1(m)
	}
	check("事件分派每事件检查规则数=1、总数=m（O(1) 查表）", o1)

	// 8. 并发只读一致
	cc := api.New(50)
	cc.Close()
	cc.RecvACK(0)
	cc.RecvFIN(7)
	want8 := fmt.Sprintf("%s/%d/%d/%d", cc.State(), cc.EnterTime(), cc.SentFIN(), cc.SentACK())
	res := make([]string, 64)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range res {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res[i] = fmt.Sprintf("%s/%d/%d/%d", cc.State(), cc.EnterTime(), cc.SentFIN(), cc.SentACK())
		}()
	}
	close(start)
	wg.Wait()
	conc := true
	for _, g := range res {
		conc = conc && g == want8
	}
	check("64 goroutine 并发只读快照逐字段相同", conc)

	// 9. SelfCheck
	check("api.SelfCheck 四条不变量", api.SelfCheck() == nil)

	if fails > 0 {
		os.Exit(1)
	}
}
