package matcher_test

import (
	"testing"

	"ontology/matcher"
)

func mustSubmit(t *testing.T, e *matcher.Engine, req matcher.NewOrderRequest) matcher.Result {
	t.Helper()
	res, err := e.Submit(req)
	if err != nil {
		t.Fatalf("submit %s: unexpected error: %v", req.ID, err)
	}
	return res
}

func codeOf(err error) matcher.ErrorCode {
	return err.(*matcher.EngineError).Code
}

// Buy and sell at exactly the same price cross; the trade price is the
// passive (maker) price.
func TestEqualPriceCrossesAtMakerPrice(t *testing.T) {
	e := matcher.NewEngine()
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "s1", Side: matcher.Sell, Price: 100, Quantity: 5, Kind: matcher.Plain})
	res := mustSubmit(t, e, matcher.NewOrderRequest{ID: "b1", Side: matcher.Buy, Price: 100, Quantity: 5, Kind: matcher.Plain})
	if len(res.Trades) != 1 {
		t.Fatalf("want 1 trade, got %d", len(res.Trades))
	}
	tr := res.Trades[0]
	if tr.Price != 100 || tr.Quantity != 5 || tr.MakerID != "s1" || tr.TakerID != "b1" {
		t.Fatalf("unexpected trade: %+v", tr)
	}
	if res.Order.Status != matcher.Filled {
		t.Fatalf("taker status = %v", res.Order.Status)
	}
	if _, ok := e.BestAsk(); ok {
		t.Fatalf("ask side should be empty")
	}
}

// An iceberg replenishes to the tail: after its first batch is consumed, the
// other order's batch trades first, then the fresh iceberg batch is reached
// within the same aggressor order.
func TestIcebergReplenishesLosesPriorityButStaysReachable(t *testing.T) {
	e := matcher.NewEngine()
	// iceberg: visible 3, total 9 -> reserve 6
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "ice", Side: matcher.Sell, Price: 10, Quantity: 9, Kind: matcher.Iceberg, DisplaySize: 3})
	// plain order joins behind the first iceberg batch
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "pln", Side: matcher.Sell, Price: 10, Quantity: 2, Kind: matcher.Plain})

	if got := e.VisibleQtyAt(matcher.Sell, 10); got != 5 {
		t.Fatalf("visible = %d, want 5", got)
	}
	res := mustSubmit(t, e, matcher.NewOrderRequest{ID: "buy", Side: matcher.Buy, Price: 10, Quantity: 11, Kind: matcher.Plain})

	wantMakers := []string{"ice", "pln", "ice", "ice"}
	wantQty := []int64{3, 2, 3, 3}
	if len(res.Trades) != len(wantMakers) {
		t.Fatalf("trades = %d, want %d: %+v", len(res.Trades), len(wantMakers), res.Trades)
	}
	for i, tr := range res.Trades {
		if tr.MakerID != wantMakers[i] || tr.Quantity != wantQty[i] || tr.Price != 10 {
			t.Fatalf("trade %d = %+v, want maker %s qty %d", i, tr, wantMakers[i], wantQty[i])
		}
	}
	ice, _ := e.GetOrder("ice")
	if ice.Remaining != 0 || ice.Visible != 0 || ice.Filled != 9 || ice.Status != matcher.Filled {
		t.Fatalf("iceberg after = %+v", ice)
	}
	if got := e.VisibleQtyAt(matcher.Sell, 10); got != 0 {
		t.Fatalf("residual visible = %d, want 0", got)
	}
}

// Hidden orders trade only after visible batches and iceberg reserve at the
// price are fully exhausted; hidden order is sequence order among themselves.
func TestHiddenTradesLast(t *testing.T) {
	e := matcher.NewEngine()
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "h1", Side: matcher.Sell, Price: 10, Quantity: 4, Kind: matcher.Hidden})
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "h2", Side: matcher.Sell, Price: 10, Quantity: 4, Kind: matcher.Hidden})
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "v1", Side: matcher.Sell, Price: 10, Quantity: 2, Kind: matcher.Plain})
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "ice", Side: matcher.Sell, Price: 10, Quantity: 4, Kind: matcher.Iceberg, DisplaySize: 2})

	if got := e.VisibleQtyAt(matcher.Sell, 10); got != 4 {
		t.Fatalf("visible excludes hidden/reserve: got %d want 4", got)
	}
	res := mustSubmit(t, e, matcher.NewOrderRequest{ID: "buy", Side: matcher.Buy, Price: 10, Quantity: 14, Kind: matcher.Plain})
	want := []struct {
		maker string
		qty   int64
	}{
		{"v1", 2}, {"ice", 2}, {"ice", 2}, {"h1", 4}, {"h2", 4},
	}
	if len(res.Trades) != len(want) {
		t.Fatalf("trades = %+v", res.Trades)
	}
	for i, w := range want {
		if res.Trades[i].MakerID != w.maker || res.Trades[i].Quantity != w.qty {
			t.Fatalf("trade %d = %+v, want %s %d", i, res.Trades[i], w.maker, w.qty)
		}
	}
}

// Decrease keeps position; increase loses position. Truncation of the current
// iceberg display batch happens in place.
func TestModifyDecreaseKeepsPositionIncreaseLoses(t *testing.T) {
	e := matcher.NewEngine()
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "a", Side: matcher.Sell, Price: 10, Quantity: 5, Kind: matcher.Plain})
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "ice", Side: matcher.Sell, Price: 10, Quantity: 10, Kind: matcher.Iceberg, DisplaySize: 3})
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "c", Side: matcher.Sell, Price: 10, Quantity: 2, Kind: matcher.Plain})

	// Decrease iceberg into its display batch: 3 visible -> 2 visible, reserve
	// wiped; queue position unchanged (still ahead of c).
	if _, err := e.Modify("ice", 2); err != nil {
		t.Fatal(err)
	}
	ice, _ := e.GetOrder("ice")
	if ice.Visible != 2 || ice.Remaining != 2 || ice.Total != 2 {
		t.Fatalf("truncated ice = %+v", ice)
	}
	// Buy 7: a(5) then ice(2); c must remain untouched because iceberg kept
	// its position ahead of c.
	res := mustSubmit(t, e, matcher.NewOrderRequest{ID: "b1", Side: matcher.Buy, Price: 10, Quantity: 7, Kind: matcher.Plain})
	wantMakers := []string{"a", "ice"}
	if len(res.Trades) != len(wantMakers) {
		t.Fatalf("trades = %+v", res.Trades)
	}
	for i, m := range wantMakers {
		if res.Trades[i].MakerID != m {
			t.Fatalf("decrease reordered queue: %+v", res.Trades)
		}
	}
	cAfter, _ := e.GetOrder("c")
	if cAfter.Remaining != 2 {
		t.Fatalf("c should not have traded, got %+v", cAfter)
	}

	// Now increase c: it must move behind the incoming order d.
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "d", Side: matcher.Sell, Price: 10, Quantity: 1, Kind: matcher.Plain})
	c0, _ := e.GetOrder("c")
	if _, err := e.Modify("c", 5); err != nil {
		t.Fatal(err)
	}
	c1, _ := e.GetOrder("c")
	if c1.Seq <= c0.Seq {
		t.Fatalf("increase kept old seq: %d -> %d", c0.Seq, c1.Seq)
	}
	res = mustSubmit(t, e, matcher.NewOrderRequest{ID: "b2", Side: matcher.Buy, Price: 10, Quantity: 10, Kind: matcher.Plain})
	// d rests ahead of the re-prioritized c.
	if res.Trades[0].MakerID != "d" {
		t.Fatalf("increased order should be behind d, got %+v", res.Trades)
	}
}

// One aggressor walks multiple price levels and trades at each maker price.
func TestAggressorCrossesMultipleLevels(t *testing.T) {
	e := matcher.NewEngine()
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "s1", Side: matcher.Sell, Price: 10, Quantity: 2, Kind: matcher.Plain})
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "s2", Side: matcher.Sell, Price: 11, Quantity: 3, Kind: matcher.Plain})
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "s3", Side: matcher.Sell, Price: 12, Quantity: 4, Kind: matcher.Plain})

	res := mustSubmit(t, e, matcher.NewOrderRequest{ID: "b", Side: matcher.Buy, Price: 11, Quantity: 7, Kind: matcher.Plain})
	if len(res.Trades) != 2 {
		t.Fatalf("trades = %+v", res.Trades)
	}
	if res.Trades[0].Price != 10 || res.Trades[0].Quantity != 2 {
		t.Fatalf("first level trade = %+v", res.Trades[0])
	}
	if res.Trades[1].Price != 11 || res.Trades[1].Quantity != 3 {
		t.Fatalf("second level trade = %+v", res.Trades[1])
	}
	if res.Order.Remaining != 2 || res.Order.Price != 11 || res.Order.Visible != 2 {
		t.Fatalf("taker remainder = %+v", res.Order)
	}
	if bid, ok := e.BestBid(); !ok || bid != 11 {
		t.Fatalf("resting bid = %d %v", bid, ok)
	}
}

// Rejected operations leave no trace: no sequence consumed, no order stored,
// book unchanged, and error priority invalid > duplicate > finished.
func TestRejectionLeavesNoTraceAndPriority(t *testing.T) {
	e := matcher.NewEngine()
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "x", Side: matcher.Sell, Price: 10, Quantity: 4, Kind: matcher.Plain})
	seqBefore := e.Stats()

	// invalid iceberg display size: even though id is also unused, invalid wins
	_, err := e.Submit(matcher.NewOrderRequest{ID: "y", Side: matcher.Sell, Price: 10, Quantity: 3, Kind: matcher.Iceberg, DisplaySize: 5})
	if codeOf(err) != matcher.ErrInvalidParameter {
		t.Fatalf("want invalid, got %v", err)
	}
	// duplicate id with otherwise-invalid params: invalid outranks duplicate
	_, err = e.Submit(matcher.NewOrderRequest{ID: "x", Side: matcher.Sell, Price: 0, Quantity: 3, Kind: matcher.Plain})
	if codeOf(err) != matcher.ErrInvalidParameter {
		t.Fatalf("invalid must outrank duplicate, got %v", err)
	}
	_, err = e.Submit(matcher.NewOrderRequest{ID: "x", Side: matcher.Sell, Price: 10, Quantity: 3, Kind: matcher.Plain})
	if codeOf(err) != matcher.ErrDuplicateID {
		t.Fatalf("want duplicate, got %v", err)
	}
	if e.Stats() != seqBefore {
		t.Fatalf("stats changed on rejection: %+v vs %+v", e.Stats(), seqBefore)
	}

	// fill x, then verify finished errors and that modify/cancel reject on
	// nonexistent ids with not-found.
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "take", Side: matcher.Buy, Price: 10, Quantity: 4, Kind: matcher.Plain})
	_, err = e.Modify("x", 2)
	if codeOf(err) != matcher.ErrOrderFinished {
		t.Fatalf("want finished, got %v", err)
	}
	_, err = e.Cancel("ghost")
	if codeOf(err) != matcher.ErrOrderNotFound {
		t.Fatalf("want not found, got %v", err)
	}
	_, err = e.Modify("ghost", 0)
	if codeOf(err) != matcher.ErrInvalidParameter {
		t.Fatalf("invalid must outrank not found, got %v", err)
	}
}

// Cancel removes resting quantity and subsequent operations report finished.
func TestCancelRemovesOrder(t *testing.T) {
	e := matcher.NewEngine()
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "s", Side: matcher.Sell, Price: 10, Quantity: 6, Kind: matcher.Plain})
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "b0", Side: matcher.Buy, Price: 10, Quantity: 2, Kind: matcher.Plain})
	o, err := e.Cancel("s")
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != matcher.Cancelled || o.Remaining != 4 || o.Filled != 2 {
		t.Fatalf("cancelled view = %+v", o)
	}
	if _, ok := e.BestAsk(); ok {
		t.Fatalf("cancelled order must be gone from the book")
	}
	if _, err := e.Cancel("s"); codeOf(err) != matcher.ErrOrderFinished {
		t.Fatalf("second cancel = %v", err)
	}
}

// Quantity conservation must hold for every order after every operation.
func TestConservationAcrossTrades(t *testing.T) {
	e := matcher.NewEngine()
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "i", Side: matcher.Sell, Price: 10, Quantity: 7, Kind: matcher.Iceberg, DisplaySize: 2})
	mustSubmit(t, e, matcher.NewOrderRequest{ID: "h", Side: matcher.Sell, Price: 10, Quantity: 3, Kind: matcher.Hidden})
	res := mustSubmit(t, e, matcher.NewOrderRequest{ID: "b", Side: matcher.Buy, Price: 10, Quantity: 6, Kind: matcher.Plain})
	var traded int64
	for _, tr := range res.Trades {
		traded += tr.Quantity
	}
	if traded != 6 {
		t.Fatalf("traded qty = %d", traded)
	}
	for _, id := range []string{"i", "h", "b"} {
		o, _ := e.GetOrder(id)
		if o.Filled+o.Remaining != o.Total {
			t.Fatalf("conservation broken for %s: %d+%d != %d", id, o.Filled, o.Remaining, o.Total)
		}
	}
}
