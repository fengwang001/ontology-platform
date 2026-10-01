package orderbook

import (
	"errors"
	"reflect"
	"testing"
)

func mustBook(t *testing.T, tick int64) *OrderBook {
	t.Helper()
	b, err := NewOrderBook(tick)
	if err != nil {
		t.Fatalf("NewOrderBook(%d): %v", tick, err)
	}
	return b
}

func mustSubmit(t *testing.T, b *OrderBook, id string, side Side, price, qty int64) []Trade {
	t.Helper()
	trades, err := b.Submit(id, side, price, qty)
	if err != nil {
		t.Fatalf("Submit(%s, %s, %d, %d): %v", id, side, price, qty, err)
	}
	return trades
}

func checkDepth(t *testing.T, b *OrderBook, side Side, n int, want []DepthLevel) {
	t.Helper()
	got := b.Depth(side, n)
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Depth(%s, %d) = %+v, want %+v", side, n, got, want)
	}
}

// 买卖价恰好相等即成交。
func TestExactPriceCrossTrades(t *testing.T) {
	b := mustBook(t, 1)
	mustSubmit(t, b, "s1", Sell, 100, 5)
	trades := mustSubmit(t, b, "b1", Buy, 100, 5)
	want := []Trade{{PassiveID: "s1", AggressiveID: "b1", Price: 100, Qty: 5}}
	if !reflect.DeepEqual(trades, want) {
		t.Fatalf("trades = %+v, want %+v", trades, want)
	}
	checkDepth(t, b, Sell, 10, nil)
	checkDepth(t, b, Buy, 10, nil)
}

// 成交价取被动方价格而非主动方价格。
func TestTradePriceIsPassive(t *testing.T) {
	b := mustBook(t, 1)
	mustSubmit(t, b, "s1", Sell, 100, 10)
	trades := mustSubmit(t, b, "b1", Buy, 105, 4)
	want := []Trade{{PassiveID: "s1", AggressiveID: "b1", Price: 100, Qty: 4}}
	if !reflect.DeepEqual(trades, want) {
		t.Fatalf("buy aggressor: trades = %+v, want %+v", trades, want)
	}

	b2 := mustBook(t, 1)
	mustSubmit(t, b2, "b1", Buy, 105, 10)
	got := mustSubmit(t, b2, "s1", Sell, 100, 3)
	want2 := []Trade{{PassiveID: "b1", AggressiveID: "s1", Price: 105, Qty: 3}}
	if !reflect.DeepEqual(got, want2) {
		t.Fatalf("sell aggressor: trades = %+v, want %+v", got, want2)
	}
}

// 同价位按到达序吃单。
func TestSamePriceFIFO(t *testing.T) {
	b := mustBook(t, 1)
	mustSubmit(t, b, "s1", Sell, 100, 2)
	mustSubmit(t, b, "s2", Sell, 100, 2)
	mustSubmit(t, b, "s3", Sell, 100, 2)
	trades := mustSubmit(t, b, "b1", Buy, 100, 5)
	want := []Trade{
		{PassiveID: "s1", AggressiveID: "b1", Price: 100, Qty: 2},
		{PassiveID: "s2", AggressiveID: "b1", Price: 100, Qty: 2},
		{PassiveID: "s3", AggressiveID: "b1", Price: 100, Qty: 1},
	}
	if !reflect.DeepEqual(trades, want) {
		t.Fatalf("trades = %+v, want %+v", trades, want)
	}
	checkDepth(t, b, Sell, 10, []DepthLevel{{Price: 100, TotalQty: 1, OrderCount: 1}})
}

// 一笔主动单跨多个价位吃光并挂出余量。
func TestSweepMultipleLevelsAndRest(t *testing.T) {
	b := mustBook(t, 1)
	mustSubmit(t, b, "s1", Sell, 100, 3)
	mustSubmit(t, b, "s2", Sell, 101, 4)
	mustSubmit(t, b, "s3", Sell, 102, 5)
	mustSubmit(t, b, "s4", Sell, 105, 7)
	trades := mustSubmit(t, b, "b1", Buy, 102, 20)
	want := []Trade{
		{PassiveID: "s1", AggressiveID: "b1", Price: 100, Qty: 3},
		{PassiveID: "s2", AggressiveID: "b1", Price: 101, Qty: 4},
		{PassiveID: "s3", AggressiveID: "b1", Price: 102, Qty: 5},
	}
	if !reflect.DeepEqual(trades, want) {
		t.Fatalf("trades = %+v, want %+v", trades, want)
	}
	// 余量 8 以 102 挂入买方簿顶；卖方 105 不动。
	checkDepth(t, b, Buy, 10, []DepthLevel{{Price: 102, TotalQty: 8, OrderCount: 1}})
	checkDepth(t, b, Sell, 10, []DepthLevel{{Price: 105, TotalQty: 7, OrderCount: 1}})
}

// 改小保位置，改大丢位置（移到同价位队尾）。
func TestAmendQueuePosition(t *testing.T) {
	b := mustBook(t, 1)
	mustSubmit(t, b, "s1", Sell, 100, 5)
	mustSubmit(t, b, "s2", Sell, 100, 5)

	// 改小：s1 保持队首。
	if err := b.Amend("s1", 2); err != nil {
		t.Fatalf("Amend shrink: %v", err)
	}
	trades := mustSubmit(t, b, "b1", Buy, 100, 3)
	want := []Trade{
		{PassiveID: "s1", AggressiveID: "b1", Price: 100, Qty: 2},
		{PassiveID: "s2", AggressiveID: "b1", Price: 100, Qty: 1},
	}
	if !reflect.DeepEqual(trades, want) {
		t.Fatalf("after shrink: trades = %+v, want %+v", trades, want)
	}

	// 改大：s2 移到队尾，此时队列为 s3(新到) 在前、s2 在后。
	mustSubmit(t, b, "s3", Sell, 100, 4)
	if err := b.Amend("s2", 9); err != nil {
		t.Fatalf("Amend grow: %v", err)
	}
	checkDepth(t, b, Sell, 10, []DepthLevel{{Price: 100, TotalQty: 4 + 9, OrderCount: 2}})
	trades2 := mustSubmit(t, b, "b2", Buy, 100, 10)
	want2 := []Trade{
		{PassiveID: "s3", AggressiveID: "b2", Price: 100, Qty: 4},
		{PassiveID: "s2", AggressiveID: "b2", Price: 100, Qty: 6},
	}
	if !reflect.DeepEqual(trades2, want2) {
		t.Fatalf("after grow: trades = %+v, want %+v", trades2, want2)
	}
}

// 改量等于当前剩余：成功且什么都不改（含排队位置）。
func TestAmendEqualIsNoOp(t *testing.T) {
	b := mustBook(t, 1)
	mustSubmit(t, b, "s1", Sell, 100, 5)
	mustSubmit(t, b, "s2", Sell, 100, 5)
	if err := b.Amend("s1", 5); err != nil {
		t.Fatalf("Amend equal: %v", err)
	}
	trades := mustSubmit(t, b, "b1", Buy, 100, 6)
	want := []Trade{
		{PassiveID: "s1", AggressiveID: "b1", Price: 100, Qty: 5},
		{PassiveID: "s2", AggressiveID: "b1", Price: 100, Qty: 1},
	}
	if !reflect.DeepEqual(trades, want) {
		t.Fatalf("trades = %+v, want %+v", trades, want)
	}
}

// 撤单：在簿、已全成交、已撤销、从未出现四种情形可区分。
func TestCancelOutcomes(t *testing.T) {
	b := mustBook(t, 1)
	mustSubmit(t, b, "s1", Sell, 100, 5)
	mustSubmit(t, b, "s2", Sell, 100, 5)
	mustSubmit(t, b, "b1", Buy, 100, 5) // 吃掉 s1，s1 全成交

	if got, err := b.Cancel("s2"); err != nil || got != 5 {
		t.Fatalf("Cancel resting = (%d, %v), want (5, nil)", got, err)
	}
	if _, err := b.Cancel("s1"); !errors.Is(err, ErrOrderFilled) {
		t.Fatalf("Cancel filled: err = %v, want ErrOrderFilled", err)
	}
	if _, err := b.Cancel("s2"); !errors.Is(err, ErrOrderCancelled) {
		t.Fatalf("Cancel cancelled: err = %v, want ErrOrderCancelled", err)
	}
	if _, err := b.Cancel("nope"); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("Cancel unknown: err = %v, want ErrUnknownID", err)
	}
	if err := b.Amend("s1", 3); !errors.Is(err, ErrOrderFilled) {
		t.Fatalf("Amend filled: err = %v, want ErrOrderFilled", err)
	}
	if err := b.Amend("nope", 3); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("Amend unknown: err = %v, want ErrUnknownID", err)
	}
}

// id 一经使用不得复用（无论已成交、已撤销还是在簿）。
func TestDuplicateIDRejected(t *testing.T) {
	b := mustBook(t, 1)
	mustSubmit(t, b, "a", Sell, 100, 5)      // 队首，将被全成交
	mustSubmit(t, b, "b", Sell, 100, 5)      // 在簿，将被撤销
	mustSubmit(t, b, "c", Buy, 100, 5)       // 吃掉 a
	if _, err := b.Cancel("b"); err != nil { // b 已撤销
		t.Fatalf("Cancel b: %v", err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if _, err := b.Submit(id, Buy, 100, 1); !errors.Is(err, ErrDuplicateID) {
			t.Fatalf("reuse id %q: err = %v, want ErrDuplicateID", id, err)
		}
	}
}

// 校验拒绝顺序与“被拒操作不改簿”。
func TestRejectionsDoNotChangeBook(t *testing.T) {
	if _, err := NewOrderBook(0); !errors.Is(err, ErrInvalidTick) {
		t.Fatalf("NewOrderBook(0): err = %v, want ErrInvalidTick", err)
	}
	if _, err := NewOrderBook(-5); !errors.Is(err, ErrInvalidTick) {
		t.Fatalf("NewOrderBook(-5): err = %v, want ErrInvalidTick", err)
	}

	b := mustBook(t, 10)
	mustSubmit(t, b, "s1", Sell, 100, 5)
	before := b.Depth(Sell, 10)

	// 拒绝顺序：id 重复 > 方向非法 > 价格非法 > 数量非法。
	if _, err := b.Submit("s1", Side(9), -1, -1); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("dup id wins: %v", err)
	}
	if _, err := b.Submit("x1", Side(9), -1, -1); !errors.Is(err, ErrInvalidSide) {
		t.Fatalf("bad side: %v", err)
	}
	for _, p := range []int64{0, -100, MaxLimit + 10, 105} { // 105 非 tick=10 倍数
		if _, err := b.Submit("x2", Buy, p, 1); !errors.Is(err, ErrInvalidPrice) {
			t.Fatalf("price %d: err = %v, want ErrInvalidPrice", p, err)
		}
	}
	for _, q := range []int64{0, -1, MaxLimit + 1} {
		if _, err := b.Submit("x3", Buy, 100, q); !errors.Is(err, ErrInvalidQty) {
			t.Fatalf("qty %d: err = %v, want ErrInvalidQty", q, err)
		}
	}
	if err := b.Amend("s1", 0); !errors.Is(err, ErrInvalidNewQty) {
		t.Fatalf("Amend 0: %v", err)
	}
	if err := b.Amend("s1", MaxLimit+1); !errors.Is(err, ErrInvalidNewQty) {
		t.Fatalf("Amend overflow: %v", err)
	}

	// 簿未变；被拒提交占用的 id 不应被登记。
	checkDepth(t, b, Sell, 10, before)
	if _, err := b.Submit("x2", Buy, 90, 1); err != nil {
		t.Fatalf("id of rejected submit must stay usable: %v", err)
	}
	checkDepth(t, b, Buy, 10, []DepthLevel{{Price: 90, TotalQty: 1, OrderCount: 1}})
}
