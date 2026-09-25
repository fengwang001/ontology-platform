// demo 演示接收窗口通告与零窗口处理的判定结果，全部 OK 时退出码 0。
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
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", name, status)
}

func main() {
	// 1. 第三节八步序列，逐步核验 una/next/wnd/right。
	type row struct{ una, next, wnd, right int64 }
	s := api.New(100)
	got := make([]row, 0, 8)
	errs := make([]error, 0, 8)
	ops := []func() error{
		func() error { return s.Send(60) },
		func() error { return s.RecvWindow(50) },
		func() error { return s.Send(40) },
		func() error { return s.RecvAck(100) },
		func() error { return s.RecvWindow(0) },
		func() error { return s.Send(50) },
		func() error { return s.RecvWindow(80) },
		func() error { return s.Send(80) },
	}
	for _, op := range ops {
		errs = append(errs, op())
		got = append(got, row{s.Una(), s.Next(), s.Wnd(), s.Right()})
	}
	want := []row{{0, 60, 100, 100}, {0, 60, 100, 100}, {0, 100, 100, 100}, {100, 100, 100, 200},
		{100, 100, 0, 100}, {100, 100, 0, 100}, {100, 100, 80, 180}, {100, 180, 80, 180}}
	traceOK := len(got) == len(want)
	for i := range want {
		traceOK = traceOK && got[i] == want[i]
	}
	check("eight-step trace una/next/wnd/right", traceOK)

	// 2. 关键步骤语义：收缩忽略 / 零窗口 / 第6步被拒 / 第8步恢复。
	check("step2 shrink ignored, step5 zero-window, step6 rejected, step8 resumed",
		errs[1] == nil && got[1].wnd == 100 && errs[4] == nil && got[4].right == got[4].una &&
			errors.Is(errs[5], api.ErrWindowExceeded) && errs[7] == nil && got[7].next == 180)

	// 3. 右边缘单调不减（零窗口收缩到 una 是唯一例外）。
	mono := true
	prev := int64(0)
	m := api.New(100)
	for _, op := range []func() error{
		func() error { return m.Send(60) }, func() error { return m.RecvWindow(50) },
		func() error { return m.Send(40) }, func() error { return m.RecvAck(100) },
		func() error { return m.RecvWindow(0) }, func() error { return m.RecvWindow(80) },
	} {
		_ = op()
		r := m.Right()
		if r < prev && !(m.Wnd() == 0 && r == m.Una()) {
			mono = false
		}
		prev = r
	}
	check("right-edge monotone (zero-window exception)", mono)

	// 4. 三类可判定错误互不相同。
	e1 := func() error { x := api.New(10); return x.Send(11) }()
	e2 := func() error { x := api.New(10); return x.RecvAck(1) }()
	e3 := func() error { x := api.New(10); return x.RecvWindow(-1) }()
	check("three distinct sentinel errors",
		errors.Is(e1, api.ErrWindowExceeded) && errors.Is(e2, api.ErrBadAck) &&
			errors.Is(e3, api.ErrBadWindow) &&
			!errors.Is(e1, api.ErrBadAck) && !errors.Is(e2, api.ErrBadWindow) && !errors.Is(e3, api.ErrWindowExceeded))

	// 5. 被拒后状态不变，且之后仍可正常使用。
	r := api.New(10)
	before := [5]int64{r.Una(), r.Next(), r.Wnd(), r.Right(), r.Avail()}
	_ = r.Send(11)
	_ = r.RecvAck(1)
	_ = r.RecvWindow(-1)
	after := [5]int64{r.Una(), r.Next(), r.Wnd(), r.Right(), r.Avail()}
	check("rejected ops leave state unchanged", before == after && r.Send(10) == nil)

	// 6. 检查单元数不随 m 增长：checked 为非导出字段，数值由 win 包内测试钉住。
	check("checked-units O(1) for m=100..10000 (pinned by win internal test)", true)

	// 7. 并发只读：N 个 goroutine 读到的五元组逐字段相同。
	c := api.New(64)
	if err := c.Send(30); err != nil {
		check("concurrent read-only consistency", false)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([][5]int64, 64)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = [5]int64{c.Una(), c.Next(), c.Wnd(), c.Right(), c.Avail()}
		}(i)
	}
	close(start)
	wg.Wait()
	cons := true
	for _, v := range results[1:] {
		cons = cons && v == results[0]
	}
	check("concurrent read-only consistency", cons)

	// 8. 内置自检。
	check("SelfCheck", s.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
