package main

import (
	"errors"
	"fmt"
	"sync"

	"ontology/align"
	"ontology/api"
	"ontology/snap"
)

func ok(label string, pass bool) {
	if pass {
		fmt.Println("OK " + label)
	} else {
		fmt.Println("FAIL " + label)
	}
}

// 第三节的八个事件（全局到达顺序）。
var eight = []api.Event{
	{Kind: api.Rec, Ch: 0, Value: 5},
	{Kind: api.Rec, Ch: 1, Value: 10},
	{Kind: api.Bar, Ch: 0, Value: 1},
	{Kind: api.Rec, Ch: 0, Value: 7},
	{Kind: api.Rec, Ch: 1, Value: 3},
	{Kind: api.Bar, Ch: 1, Value: 1},
	{Kind: api.Rec, Ch: 0, Value: 2},
	{Kind: api.Rec, Ch: 1, Value: 1},
}

func main() {
	// align：单通道阻塞标记 / 在途缓冲 / 屏障 id 追踪
	var c align.Channel
	c.Block(1)
	buffered := c.Blocked() && c.BarrierID() == 1 && c.LastBarrier() == 1
	c.Buffer(7)
	d := c.Drain()
	c.Unblock()
	ok("align block/buffer/drain/unblock", buffered && len(d) == 1 && d[0] == 7 &&
		len(c.Pending()) == 0 && !c.Blocked())

	// snap：八事件逐步 sum、第 3/4/6 步判定、snap[1]、终值
	e, _ := snap.NewEngine(2)
	sv := []snap.Event{
		{Kind: snap.Rec, Ch: 0, V: 5}, {Kind: snap.Rec, Ch: 1, V: 10},
		{Kind: snap.Bar, Ch: 0, V: 1}, {Kind: snap.Rec, Ch: 0, V: 7},
		{Kind: snap.Rec, Ch: 1, V: 3}, {Kind: snap.Bar, Ch: 1, V: 1},
		{Kind: snap.Rec, Ch: 0, V: 2}, {Kind: snap.Rec, Ch: 1, V: 1},
	}
	want := []int{5, 15, 15, 15, 18, 25, 27, 28}
	stepwise, gate34, snap6 := true, true, false
	for i, ev := range sv {
		_ = e.Feed([]snap.Event{ev})
		stepwise = stepwise && e.Sum() == want[i]
		if i == 2 || i == 3 {
			_, has := e.Snapshot(1)
			gate34 = gate34 && e.Sum() == 15 && !has
		}
		if i == 5 {
			v, has := e.Snapshot(1)
			snap6 = has && v == 18 && e.Sum() == 25
		}
	}
	ok("snap eight-step sums 5..28", stepwise)
	ok("snap steps 3/4 gated, step6 snap=18", gate34 && snap6 && e.Sum() == 28)

	// api：SelfCheck 四条不变量
	a, _ := api.New(2)
	ok("api SelfCheck invariants 1..4", a.SelfCheck() == nil)

	// 四类可判定错误，互不相同
	_, errN := api.New(0)
	a0, _ := api.New(2)
	errRange := a0.Feed([]api.Event{{Kind: api.Rec, Ch: 99, Value: 1}})
	errPos := a0.Feed([]api.Event{{Kind: api.Bar, Ch: 0, Value: 0}})
	errOrder := a0.Feed([]api.Event{{Kind: api.Bar, Ch: 0, Value: 2}, {Kind: api.Bar, Ch: 0, Value: 1}})
	distinct := errN != errRange && errN != errPos && errN != errOrder &&
		errRange != errPos && errRange != errOrder && errPos != errOrder
	ok("api four distinct sentinel errors", errors.Is(errN, api.ErrInvalidChannels) &&
		errors.Is(errRange, api.ErrChannelOutOfRange) &&
		errors.Is(errPos, api.ErrBarrierNonPositive) &&
		errors.Is(errOrder, api.ErrBarrierOutOfOrder) && distinct)

	// 被拒整批不留痕：+999 与非法屏障同批，sum 必须不变
	_ = a.Feed(eight)
	before := a.Sum()
	rejected := a.Feed([]api.Event{{Kind: api.Rec, Ch: 0, Value: 999}, {Kind: api.Bar, Ch: 0, Value: 0}})
	ok("api rejected batch leaves no trace", rejected != nil && a.Sum() == before && before == 28)

	// 大 m 下完成判定检查的通道数不随 m 增长（只读布尔谓词，读不到计数器数值）
	o1 := true
	for _, m := range []int{100, 1000, 10000} {
		g, _ := snap.NewEngine(m)
		for ch := 0; ch < m; ch++ {
			_ = g.Feed([]snap.Event{{Kind: snap.Bar, Ch: ch, V: 1}})
		}
		v, has := g.Snapshot(1)
		o1 = o1 && has && v == 0 && g.LastCheckConstantBound()
	}
	ok("snap O(1) alignment check at m=100..10000", o1)

	// 并发只读：N 个 goroutine 的 Sum/Snapshot 必须逐字段相同
	const n = 64
	var wg sync.WaitGroup
	type res struct {
		sum, snap int
		has       bool
	}
	resCh := make(chan res, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := a.Sum()
			v, has := a.Snapshot(1)
			resCh <- res{s, v, has}
		}()
	}
	wg.Wait()
	close(resCh)
	first := <-resCh
	same := first.sum == 28 && first.snap == 18 && first.has
	for r := range resCh {
		same = same && r == first
	}
	ok("api concurrent readers identical", same)
}
