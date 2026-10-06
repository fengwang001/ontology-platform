package matching

import "testing"

func mustSubmit(t *testing.T, e *Engine, p OrderParams) (int64, []Fill) {
	t.Helper()
	seq, fills, err := e.Submit(p)
	if err != nil {
		t.Fatalf("submit %d unexpected error: %v", p.ClientID, err)
	}
	return seq, fills
}

func expectErrKind(t *testing.T, err error, want ErrorKind) {
	t.Helper()
	ee, ok := err.(*EngineError)
	if !ok {
		t.Fatalf("want *EngineError, got %T: %v", err, err)
	}
	if ee.Kind != want {
		t.Fatalf("want error kind %d, got %d (%v)", want, ee.Kind, err)
	}
}

// 买卖价恰等成交，成交价取被动方价格。
func TestCrossAtEqualPrice(t *testing.T) {
	e := New()
	mustSubmit(t, e, OrderParams{ClientID: 1, Side: Sell, Price: 100, TotalQty: 10, Type: Limit})
	_, fills, err := e.Submit(OrderParams{ClientID: 2, Side: Buy, Price: 100, TotalQty: 4, Type: Limit})
	if err != nil {
		t.Fatal(err)
	}
	if len(fills) != 1 || fills[0].Price != 100 || fills[0].Qty != 4 {
		t.Fatalf("unexpected fills: %+v", fills)
	}
	if fills[0].BuyClientID != 2 || fills[0].SellClientID != 1 {
		t.Fatalf("wrong sides: %+v", fills[0])
	}
	if _, ok := e.BestBid(); ok {
		t.Fatal("buy should be fully filled")
	}
	if ask, ok := e.BestAsk(); !ok || ask != 100 {
		t.Fatalf("best ask want 100, got %d/%v", ask, ok)
	}
	if d := e.VisibleDepthAt(Sell, 100); d != 6 {
		t.Fatalf("ask depth want 6, got %d", d)
	}
	if o, _ := e.GetOrder(1); o.Status != StatusPartial || o.FilledQty != 4 || o.RemainingQty != 6 || o.TotalQty != 10 {
		t.Fatalf("seller snapshot wrong: %+v", o)
	}
}

// 冰山补充后失去优先级，但在同一次吃单中可被继续成交。
func TestIcebergReplenishLosesPriority(t *testing.T) {
	e := New()
	mustSubmit(t, e, OrderParams{ClientID: 1, Side: Sell, Price: 100, TotalQty: 10, Type: Iceberg, IcebergVisibleQty: 3})
	mustSubmit(t, e, OrderParams{ClientID: 2, Side: Sell, Price: 100, TotalQty: 4, Type: Limit})
	if d := e.VisibleDepthAt(Sell, 100); d != 7 {
		t.Fatalf("initial visible depth want 7, got %d", d)
	}
	_, fills, err := e.Submit(OrderParams{ClientID: 3, Side: Buy, Price: 100, TotalQty: 20, Type: Limit})
	if err != nil {
		t.Fatal(err)
	}
	wantSeller := []int64{1, 2, 1, 1, 1}
	wantQty := []int64{3, 4, 3, 3, 1}
	if len(fills) != len(wantSeller) {
		t.Fatalf("want %d fills, got %d: %+v", len(wantSeller), len(fills), fills)
	}
	for i := range wantSeller {
		if fills[i].SellClientID != wantSeller[i] || fills[i].Qty != wantQty[i] {
			t.Fatalf("fill %d want seller=%d qty=%d, got seller=%d qty=%d",
				i, wantSeller[i], wantQty[i], fills[i].SellClientID, fills[i].Qty)
		}
	}
	o1, _ := e.GetOrder(1)
	o2, _ := e.GetOrder(2)
	if o1.Status != StatusCompleted || o2.Status != StatusCompleted {
		t.Fatalf("both should complete: %+v %+v", o1, o2)
	}
	if _, ok := e.BestAsk(); ok {
		t.Fatal("ask side should be empty")
	}
}

// 仅当显示队列空且冰山储备耗尽，才吃隐藏委托；隐藏之间按序号。
func TestHiddenFillsLast(t *testing.T) {
	e := New()
	mustSubmit(t, e, OrderParams{ClientID: 1, Side: Sell, Price: 100, TotalQty: 5, Type: Limit})
	mustSubmit(t, e, OrderParams{ClientID: 2, Side: Sell, Price: 100, TotalQty: 6, Type: Iceberg, IcebergVisibleQty: 2})
	mustSubmit(t, e, OrderParams{ClientID: 3, Side: Sell, Price: 100, TotalQty: 7, Type: Hidden})
	mustSubmit(t, e, OrderParams{ClientID: 4, Side: Sell, Price: 100, TotalQty: 8, Type: Hidden})
	if d := e.VisibleDepthAt(Sell, 100); d != 7 {
		t.Fatalf("visible depth must exclude hidden: want 7 got %d", d)
	}
	_, fills, _ := e.Submit(OrderParams{ClientID: 5, Side: Buy, Price: 100, TotalQty: 30, Type: Limit})
	wantSeller := []int64{1, 2, 2, 2, 3, 4}
	wantQty := []int64{5, 2, 2, 2, 7, 8}
	if len(fills) != len(wantSeller) {
		t.Fatalf("fills=%+v", fills)
	}
	for i := range wantSeller {
		if fills[i].SellClientID != wantSeller[i] || fills[i].Qty != wantQty[i] {
			t.Fatalf("fill %d got seller=%d qty=%d, want seller=%d qty=%d",
				i, fills[i].SellClientID, fills[i].Qty, wantSeller[i], wantQty[i])
		}
	}
	_, fills2, _ := e.Submit(OrderParams{ClientID: 6, Side: Buy, Price: 100, TotalQty: 3, Type: Limit})
	if len(fills2) != 0 {
		t.Fatalf("book should be empty, fills=%+v", fills2)
	}
}

// 减量保留排队位置；加量失去优先级排到队尾；显示批截短。
func TestReplacePriority(t *testing.T) {
	e := New()
	mustSubmit(t, e, OrderParams{ClientID: 1, Side: Sell, Price: 100, TotalQty: 10, Type: Iceberg, IcebergVisibleQty: 3})
	mustSubmit(t, e, OrderParams{ClientID: 2, Side: Sell, Price: 100, TotalQty: 4, Type: Limit})

	if err := e.ReplaceQty(1, 2); err != nil {
		t.Fatal(err)
	}
	if d := e.VisibleDepthAt(Sell, 100); d != 6 {
		t.Fatalf("after shrink depth want 6, got %d", d)
	}
	o1, _ := e.GetOrder(1)
	if o1.RemainingQty != 2 || o1.TotalQty != 2 {
		t.Fatalf("A total/remaining wrong: %+v", o1)
	}
	_, fills, _ := e.Submit(OrderParams{ClientID: 3, Side: Buy, Price: 100, TotalQty: 3, Type: Limit})
	if fills[0].SellClientID != 1 || fills[0].Qty != 2 || fills[1].SellClientID != 2 || fills[1].Qty != 1 {
		t.Fatalf("decrease keeps priority, got %+v", fills)
	}

	mustSubmit(t, e, OrderParams{ClientID: 4, Side: Sell, Price: 100, TotalQty: 2, Type: Limit})
	if err := e.ReplaceQty(2, 10); err != nil {
		t.Fatal(err)
	}
	o2, _ := e.GetOrder(2)
	o4, _ := e.GetOrder(4)
	if o2.Seq <= o4.Seq {
		t.Fatalf("increased order must get newer seq: B=%d C=%d", o2.Seq, o4.Seq)
	}
	_, fills, _ = e.Submit(OrderParams{ClientID: 5, Side: Buy, Price: 100, TotalQty: 5, Type: Limit})
	if fills[0].SellClientID != 4 || fills[0].Qty != 2 || fills[1].SellClientID != 2 || fills[1].Qty != 3 {
		t.Fatalf("increase loses priority, got %+v", fills)
	}
}

// 撤单移除委托，不影响已成交；已完成/已撤销不可再操作。
func TestCancel(t *testing.T) {
	e := New()
	mustSubmit(t, e, OrderParams{ClientID: 1, Side: Sell, Price: 100, TotalQty: 5, Type: Limit})
	mustSubmit(t, e, OrderParams{ClientID: 2, Side: Sell, Price: 101, TotalQty: 5, Type: Limit})
	if err := e.Cancel(1); err != nil {
		t.Fatal(err)
	}
	if ask, ok := e.BestAsk(); !ok || ask != 101 {
		t.Fatalf("best ask after cancel want 101, got %d/%v", ask, ok)
	}
	if o, _ := e.GetOrder(1); o.Status != StatusCancelled {
		t.Fatalf("want cancelled, got %d", o.Status)
	}
	expectErrKind(t, e.Cancel(1), ErrOrderCancelled)
	expectErrKind(t, e.Cancel(99), ErrOrderNotFound)
	_, fills, _ := e.Submit(OrderParams{ClientID: 3, Side: Buy, Price: 101, TotalQty: 5, Type: Limit})
	if len(fills) != 1 {
		t.Fatalf("want 1 fill, got %+v", fills)
	}
	expectErrKind(t, e.Cancel(2), ErrOrderCompleted)
	expectErrKind(t, e.ReplaceQty(2, 1), ErrOrderCompleted)
}

// 一次主动委托跨多个价位，且不越过不交叉的价位。
func TestSweepMultipleLevels(t *testing.T) {
	e := New()
	mustSubmit(t, e, OrderParams{ClientID: 1, Side: Sell, Price: 100, TotalQty: 2, Type: Limit})
	mustSubmit(t, e, OrderParams{ClientID: 2, Side: Sell, Price: 101, TotalQty: 3, Type: Limit})
	mustSubmit(t, e, OrderParams{ClientID: 3, Side: Sell, Price: 102, TotalQty: 4, Type: Limit})
	_, fills, _ := e.Submit(OrderParams{ClientID: 4, Side: Buy, Price: 101, TotalQty: 7, Type: Limit})
	if len(fills) != 2 || fills[0].Price != 100 || fills[0].Qty != 2 ||
		fills[1].Price != 101 || fills[1].Qty != 3 {
		t.Fatalf("sweep should cross 100 and 101 only: %+v", fills)
	}
	if ask, ok := e.BestAsk(); !ok || ask != 102 {
		t.Fatalf("remaining ask want 102, got %d/%v", ask, ok)
	}
	o4, _ := e.GetOrder(4)
	if o4.RemainingQty != 2 || o4.Status != StatusPartial {
		t.Fatalf("aggressor remaining wrong: %+v", o4)
	}
	if d := e.VisibleDepthAt(Buy, 101); d != 2 {
		t.Fatalf("aggressor should rest 2@101 visible, got %d", d)
	}
}

// 主动冰山：只有挂簿部分才有显示量。
func TestAggressiveIcebergRestingVisible(t *testing.T) {
	e := New()
	mustSubmit(t, e, OrderParams{ClientID: 1, Side: Sell, Price: 100, TotalQty: 2, Type: Limit})
	_, fills, _ := e.Submit(OrderParams{ClientID: 2, Side: Buy, Price: 100, TotalQty: 10, Type: Iceberg, IcebergVisibleQty: 3})
	if len(fills) != 1 || fills[0].Qty != 2 {
		t.Fatalf("fills=%+v", fills)
	}
	if d := e.VisibleDepthAt(Buy, 100); d != 3 {
		t.Fatalf("resting iceberg visible batch want 3, got %d", d)
	}
	o2, _ := e.GetOrder(2)
	if o2.RemainingQty != 8 || o2.Status != StatusPartial {
		t.Fatalf("resting iceberg wrong: %+v", o2)
	}
}
