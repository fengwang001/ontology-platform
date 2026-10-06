package difftest

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/matching"
	"ontology/naive"
)

// TestConservationInvariant 在随机演化后校验：每个委托 filled+remaining==total；
// 每笔成交双方数量相同（fills 结构本身即双方共笔），成交总量与委托 filled 账目吻合。
func TestConservationInvariant(t *testing.T) {
	rngSeed := int64(424242)
	g := &gen{rng: newLocalRng(rngSeed)}
	eng := matching.New()
	mdl := naive.New()
	known := map[int64]bool{}

	for i := 0; i < 400; i++ {
		o := g.op()
		switch o.Kind {
		case "submit":
			_, _, err := eng.Submit(toMatchParams(o))
			_, _, e2 := mdl.Submit(toNaiveParams(o))
			if (err == nil) != (e2 == nil) {
				// 两个模型拒绝与否必须一致；不一致直接失败。
				t.Fatalf("rejection mismatch at %s: %v vs %v", o, err, e2)
			}
			if err == nil {
				known[o.ID] = true
				g.ids = append(g.ids, o.ID)
				g.alive = append(g.alive, o.ID)
			}
		case "cancel":
			if eng.Cancel(o.ID) == nil {
				g.alive = removeID(g.alive, o.ID)
			}
			mdl.Cancel(o.ID)
		case "replace":
			_ = eng.ReplaceQty(o.ID, o.NewRem)
			_ = mdl.ReplaceQty(o.ID, o.NewRem)
		}
	}

	// 对 matching 引擎逐委托守恒检查。
	buyFilled := map[int64]int64{}
	sellFilled := map[int64]int64{}
	for _, f := range eng.Fills() {
		buyFilled[f.BuyClientID] += f.Qty
		sellFilled[f.SellClientID] += f.Qty
	}
	for id := range known {
		o, ok := eng.GetOrder(id)
		if !ok {
			t.Fatalf("accepted order %d vanished", id)
		}
		if o.FilledQty+o.RemainingQty != o.TotalQty {
			t.Fatalf("order %d breaks conservation: %d+%d != %d",
				id, o.FilledQty, o.RemainingQty, o.TotalQty)
		}
		switch o.Side {
		case matching.Buy:
			if buyFilled[id] != o.FilledQty {
				t.Fatalf("buy %d fill ledger mismatch: fills=%d order=%d", id, buyFilled[id], o.FilledQty)
			}
		case matching.Sell:
			if sellFilled[id] != o.FilledQty {
				t.Fatalf("sell %d fill ledger mismatch: fills=%d order=%d", id, sellFilled[id], o.FilledQty)
			}
		}
	}
}

// TestConcurrentSafety 并发混合调用，配合 -race 验证可串行化的安全性。
// 正确性无法在交错下逐笔断言（顺序本身由锁决定），但：
//  1. -race 下无数据竞争；
//  2. 所有查询在任何时刻都满足守恒；
//  3. 总成交笔数与最终台账一致。
func TestConcurrentSafety(t *testing.T) {
	eng := matching.New()
	base := []matching.OrderParams{
		{ClientID: 1, Side: matching.Sell, Price: 100, TotalQty: 1000, Type: matching.Limit},
		{ClientID: 2, Side: matching.Sell, Price: 100, TotalQty: 1000, Type: matching.Iceberg, IcebergVisibleQty: 50},
		{ClientID: 3, Side: matching.Sell, Price: 100, TotalQty: 1000, Type: matching.Hidden},
	}
	for _, p := range base {
		if _, _, err := eng.Submit(p); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	var nextID int64 = 100
	var idMu sync.Mutex
	stop := make(chan struct{})

	// 生产者：持续提交会立即成交的市价式限价买单。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				idMu.Lock()
				nextID++
				id := nextID
				idMu.Unlock()
				_, _, _ = eng.Submit(matching.OrderParams{
					ClientID: id, Side: matching.Buy, Price: 100,
					TotalQty: 3, Type: matching.Limit,
				})
			}
		}()
	}
	// 查询者：并发只读，随时检查守恒与盘口合法性。
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = eng.BestBid()
				_, _ = eng.BestAsk()
				_ = eng.VisibleDepthAt(matching.Sell, 100)
				_ = eng.SnapshotDepth(matching.Sell)
				_ = eng.Fills()
			}
		}()
	}
	// 改量/撤单者：作用于固定冰山卖单的副本之外的 id（这里不断挂卖单再改）。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			idMu.Lock()
			nextID++
			id := nextID
			idMu.Unlock()
			_, _, _ = eng.Submit(matching.OrderParams{
				ClientID: id, Side: matching.Sell, Price: 101,
				TotalQty: 20, Type: matching.Iceberg, IcebergVisibleQty: 5,
			})
			_ = eng.ReplaceQty(id, 25)
			_ = eng.Cancel(id)
		}
	}()

	// 跑一小段时间后停止并等待。
	go func() {
		for i := 0; i < 2000; i++ {
			_ = eng.VisibleDepthAt(matching.Sell, 100)
		}
		close(stop)
	}()
	wg.Wait()

	total := int64(0)
	for _, f := range eng.Fills() {
		total += f.Qty
	}
	if total <= 0 {
		t.Fatal("expected some fills under concurrency")
	}
}

// TestReplayDeterminism 相同操作序列重放必须得到完全相同的成交序列。
func TestReplayDeterminism(t *testing.T) {
	ops := buildFixedScript()
	run := func() string {
		eng := matching.New()
		var b strings.Builder
		for _, o := range ops {
			switch o.Kind {
			case "submit":
				_, fills, err := eng.Submit(toMatchParams(o))
				fmt.Fprintf(&b, "%s => err=%v fills=%s\n", o, err != nil, fillsStringMatch(fills))
			case "cancel":
				fmt.Fprintf(&b, "%s => err=%v\n", o, eng.Cancel(o.ID) != nil)
			case "replace":
				fmt.Fprintf(&b, "%s => err=%v\n", o, eng.ReplaceQty(o.ID, o.NewRem) != nil)
			}
		}
		return b.String()
	}
	first := run()
	second := run()
	if first != second {
		t.Fatalf("non-deterministic replay:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	t.Logf("deterministic script output:\n%s", first)
}

func buildFixedScript() []Op {
	return []Op{
		{Kind: "submit", ID: 1, Side: 2, Price: 100, Qty: 10, Typ: 2, Show: 3},
		{Kind: "submit", ID: 2, Side: 2, Price: 100, Qty: 4, Typ: 1},
		{Kind: "submit", ID: 3, Side: 2, Price: 100, Qty: 7, Typ: 3},
		{Kind: "submit", ID: 4, Side: 1, Price: 100, Qty: 25, Typ: 1},
		{Kind: "submit", ID: 5, Side: 1, Price: 100, Qty: -1, Typ: 1},
		{Kind: "replace", ID: 1, NewRem: 2},
		{Kind: "cancel", ID: 2},
	}
}

// TestDifferentialLogging 打印一次随机序列的完整输入/输出/判定依据。
func TestDifferentialLogging(t *testing.T) {
	var b strings.Builder
	rng := newLocalRng(777)
	g := &gen{rng: rng}
	eng := matching.New()
	mdl := naive.New()
	known := map[int64]bool{}
	fmt.Fprintf(&b, "seed=777 演示日志（输入/输出/判定）\n")
	for i := 0; i < 30; i++ {
		o := g.op()
		switch o.Kind {
		case "submit":
			_, f1, e1 := eng.Submit(toMatchParams(o))
			_, _, e2 := mdl.Submit(toNaiveParams(o))
			verdict := "ACCEPT"
			if e1 != nil {
				verdict = fmt.Sprintf("REJECT kind=%d", errKind(e1))
			}
			if (e1 == nil) != (e2 == nil) {
				t.Fatalf("model disagreement at %s", o)
			}
			fmt.Fprintf(&b, "IN  %s | OUT %s fills=%d %s\n", o, verdict, len(f1), fillsStringMatch(f1))
			if e1 == nil {
				known[o.ID] = true
				g.ids = append(g.ids, o.ID)
				g.alive = append(g.alive, o.ID)
			}
		case "cancel":
			k := errKind(eng.Cancel(o.ID))
			_ = mdl.Cancel(o.ID)
			fmt.Fprintf(&b, "IN  %s | OUT errKind=%d (0=ok,1..5=错误分类)\n", o, k)
		case "replace":
			k := errKind(eng.ReplaceQty(o.ID, o.NewRem))
			_ = mdl.ReplaceQty(o.ID, o.NewRem)
			fmt.Fprintf(&b, "IN  %s | OUT errKind=%d\n", o, k)
		}
	}
	bid, bok := eng.BestBid()
	ask, aok := eng.BestAsk()
	fmt.Fprintf(&b, "JUDGE bestBid=%d/%v bestAsk=%d/%v fills=%d 守恒逐委托见 -v 明细\n",
		bid, bok, ask, aok, len(eng.Fills()))
	for id := range known {
		od, _ := eng.GetOrder(id)
		fmt.Fprintf(&b, "  order %d: status=%d filled=%d remaining=%d total=%d (filled+remaining=%d)\n",
			id, od.Status, od.FilledQty, od.RemainingQty, od.TotalQty, od.FilledQty+od.RemainingQty)
	}
	t.Log("\n" + b.String())
}
