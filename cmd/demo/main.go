// demo 逐步核验接收窗口通告与零窗口处理的各项判定，全部 OK 时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"

	"ontology/api"
)

var failed bool

func ok(name string, cond bool) {
	mark := "OK   "
	if !cond {
		mark = "FAIL "
		failed = true
	}
	fmt.Println(mark + name)
}

// checkedOf 用反射读取 win.Window 的非导出计数器（公开接口不暴露它）。
func checkedOf(s *api.Sender) int64 {
	return reflect.ValueOf(s).Elem().FieldByName("f").Elem().
		FieldByName("w").FieldByName("checked").Int()
}

func main() {
	// 第三节八个操作，逐步核对 una/next/wnd/right。
	type state struct{ una, next, wnd, right int64 }
	ops := []struct {
		run   func(*api.Sender) error
		want  state
		isErr error
	}{
		{func(s *api.Sender) error { return s.Send(60) }, state{0, 60, 100, 100}, nil},
		{func(s *api.Sender) error { return s.RecvWindow(50) }, state{0, 60, 100, 100}, nil},
		{func(s *api.Sender) error { return s.Send(40) }, state{0, 100, 100, 100}, nil},
		{func(s *api.Sender) error { return s.RecvAck(100) }, state{100, 100, 100, 200}, nil},
		{func(s *api.Sender) error { return s.RecvWindow(0) }, state{100, 100, 0, 100}, nil},
		{func(s *api.Sender) error { return s.Send(50) }, state{100, 100, 0, 100}, api.ErrWindowExceeded},
		{func(s *api.Sender) error { return s.RecvWindow(80) }, state{100, 100, 80, 180}, nil},
		{func(s *api.Sender) error { return s.Send(80) }, state{100, 180, 80, 180}, nil},
	}
	s := api.New(100)
	stepsOK, shrinkIgnored, zeroWnd, rejected, recovered := true, false, false, false, false
	for i, op := range ops {
		err := op.run(s)
		got := state{s.Una(), s.Next(), s.Wnd(), s.Right()}
		if !errors.Is(err, op.isErr) || got != op.want {
			stepsOK = false
			fmt.Printf("  step %d: got %+v err=%v\n", i+1, got, err)
		}
		switch i {
		case 1:
			shrinkIgnored = s.Wnd() == 100 && s.Right() == 100
		case 4:
			zeroWnd = s.Wnd() == 0 && s.Right() == s.Una()
		case 5:
			rejected = errors.Is(err, api.ErrWindowExceeded)
		case 7:
			recovered = err == nil && s.Next() == 180
		}
	}
	ok("八步操作每步 una/next/wnd/right 与手推一致", stepsOK)
	ok("第2步非零收缩被忽略(wnd=100,right=100)", shrinkIgnored)
	ok("第5步零窗口被接受(wnd=0,right=una)", zeroWnd)
	ok("第6步零窗口期发送被拒 ErrWindowExceeded", rejected)
	ok("第8步窗口更新后恢复发送", recovered)

	// 右边缘单调不减，零窗口收缩到 una 是唯一例外。
	m := api.New(100)
	_ = m.Send(100)
	_ = m.RecvAck(100)
	prev, mono := m.Right(), true
	for _, w := range []int64{150, 30, 0, 80} {
		_ = m.RecvWindow(w)
		r := m.Right()
		if (w == 0 && r != m.Una()) || (w > 0 && r < prev) {
			mono = false
		}
		prev = r
	}
	ok("右边缘单调不减(零窗口收缩到una为例外)", mono)

	// 三类哨兵错误互不相同。
	distinct := !errors.Is(api.ErrWindowExceeded, api.ErrBadAck) &&
		!errors.Is(api.ErrBadAck, api.ErrBadWindow) &&
		!errors.Is(api.ErrBadWindow, api.ErrWindowExceeded)
	e := api.New(10)
	_ = e.Send(10)
	distinct = distinct && errors.Is(e.Send(1), api.ErrWindowExceeded) &&
		errors.Is(e.RecvAck(11), api.ErrBadAck) && errors.Is(e.RecvWindow(-1), api.ErrBadWindow)
	ok("三类错误可判定且互不相同", distinct)

	// 被拒后状态不变，且之后仍可正常使用。
	before := [5]int64{e.Una(), e.Next(), e.Wnd(), e.Right(), e.Avail()}
	_ = e.Send(1)
	_ = e.RecvAck(11)
	_ = e.RecvWindow(-1)
	after := [5]int64{e.Una(), e.Next(), e.Wnd(), e.Right(), e.Avail()}
	ok("被拒操作不留痕且实例仍可用", before == after && e.RecvAck(10) == nil && e.Send(5) == nil)

	// 大 m 下检查个数不随 m 增长（反射读非导出计数器）。
	o1 := true
	for _, m64 := range []int64{100, 1000, 10000} {
		c := api.New(m64)
		for i := int64(0); i < m64; i++ {
			_ = c.Send(1)
		}
		_ = c.RecvAck(m64)
		if checkedOf(c) > 2 {
			o1 = false
		}
	}
	ok("检查个数不随在途规模m增长(O(1))", o1)

	// 并发只读：N 个 goroutine 逐字段一致。
	c := api.New(1000)
	_ = c.Send(500)
	want := [5]int64{c.Una(), c.Next(), c.Wnd(), c.Right(), c.Avail()}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	raceOK := true
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got := [5]int64{c.Una(), c.Next(), c.Wnd(), c.Right(), c.Avail()}
			if got != want {
				mu.Lock()
				raceOK = false
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	ok("并发只读逐字段一致", raceOK && c.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
