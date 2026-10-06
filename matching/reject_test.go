package matching

import "testing"

// 被拒绝操作不留痕：参数非法优先于编号重复；拒绝不占序号、不改任何状态。
func TestRejectedSubmitLeavesNoTrace(t *testing.T) {
	e := New()
	seq1, _ := mustSubmit(t, e, OrderParams{ClientID: 1, Side: Buy, Price: 100, TotalQty: 5, Type: Limit})

	// 参数非法优先级高于编号重复（同编号且参数也非法）。
	_, _, err := e.Submit(OrderParams{ClientID: 1, Side: Buy, Price: 100, TotalQty: -1, Type: Limit})
	expectErrKind(t, err, ErrInvalidParam)

	// 冰山显示量大于总量非法。
	_, _, err = e.Submit(OrderParams{ClientID: 7, Side: Buy, Price: 100, TotalQty: 5, Type: Iceberg, IcebergVisibleQty: 6})
	expectErrKind(t, err, ErrInvalidParam)

	// 非法方向/价格。
	_, _, err = e.Submit(OrderParams{ClientID: 8, Side: Side(9), Price: 100, TotalQty: 5, Type: Limit})
	expectErrKind(t, err, ErrInvalidParam)
	_, _, err = e.Submit(OrderParams{ClientID: 9, Side: Buy, Price: 0, TotalQty: 5, Type: Limit})
	expectErrKind(t, err, ErrInvalidParam)

	// 编号重复。
	_, _, err = e.Submit(OrderParams{ClientID: 1, Side: Buy, Price: 100, TotalQty: 5, Type: Limit})
	expectErrKind(t, err, ErrDuplicateClientID)

	// 被拒绝的 7/8/9 不占序号：下一个接受的委托序号应为 2。
	seq2, _ := mustSubmit(t, e, OrderParams{ClientID: 2, Side: Buy, Price: 99, TotalQty: 1, Type: Limit})
	if seq1 != 1 || seq2 != 2 {
		t.Fatalf("rejected submits must not consume seq: %d %d", seq1, seq2)
	}

	// 盘口与深度未因拒绝而改变。
	if bid, ok := e.BestBid(); !ok || bid != 100 {
		t.Fatalf("best bid want 100, got %d/%v", bid, ok)
	}
	if e.VisibleDepthAt(Buy, 100) != 5 || e.VisibleDepthAt(Buy, 99) != 1 {
		t.Fatal("depth changed by rejected submit")
	}
	if len(e.Fills()) != 0 {
		t.Fatal("rejected submit must not produce fills")
	}

	// 改量参数非法优先于委托不存在。
	err = e.ReplaceQty(12345, 0)
	expectErrKind(t, err, ErrInvalidParam)
	err = e.ReplaceQty(12345, 1)
	expectErrKind(t, err, ErrOrderNotFound)
}

// 隐藏委托减量时隐藏量仍不进入显示汇总；隐藏加量排到隐藏队尾。
func TestHiddenReplace(t *testing.T) {
	e := New()
	mustSubmit(t, e, OrderParams{ClientID: 1, Side: Sell, Price: 100, TotalQty: 5, Type: Limit})
	mustSubmit(t, e, OrderParams{ClientID: 2, Side: Sell, Price: 100, TotalQty: 9, Type: Hidden})
	mustSubmit(t, e, OrderParams{ClientID: 3, Side: Sell, Price: 100, TotalQty: 9, Type: Hidden})

	if err := e.ReplaceQty(2, 4); err != nil {
		t.Fatal(err)
	}
	if d := e.VisibleDepthAt(Sell, 100); d != 5 {
		t.Fatalf("hidden qty must not show, got %d", d)
	}
	// 2 号加量：新序号更大，应排到 3 号之后。
	if err := e.ReplaceQty(2, 6); err != nil {
		t.Fatal(err)
	}
	_, fills, _ := e.Submit(OrderParams{ClientID: 4, Side: Buy, Price: 100, TotalQty: 20, Type: Limit})
	// 先普通 5，再隐藏 3(9)，再隐藏 2(6)
	wantSeller := []int64{1, 3, 2}
	wantQty := []int64{5, 9, 6}
	if len(fills) != 3 {
		t.Fatalf("fills=%+v", fills)
	}
	for i := range wantSeller {
		if fills[i].SellClientID != wantSeller[i] || fills[i].Qty != wantQty[i] {
			t.Fatalf("fill %d got seller=%d qty=%d", i, fills[i].SellClientID, fills[i].Qty)
		}
	}
}

// 冰山隐藏储备不进入显示汇总，但总量/剩余量可查询。
func TestInvisibleReserveQuery(t *testing.T) {
	e := New()
	mustSubmit(t, e, OrderParams{ClientID: 1, Side: Sell, Price: 100, TotalQty: 10, Type: Iceberg, IcebergVisibleQty: 2})
	mustSubmit(t, e, OrderParams{ClientID: 2, Side: Sell, Price: 100, TotalQty: 4, Type: Hidden})
	if d := e.VisibleDepthAt(Sell, 100); d != 2 {
		t.Fatalf("only current batch visible, got %d", d)
	}
	depth := e.SnapshotDepth(Sell)
	if len(depth) != 1 || depth[0].VisibleQty != 2 {
		t.Fatalf("snapshot depth wrong: %+v", depth)
	}
	o2, _ := e.GetOrder(2)
	if o2.RemainingQty != 4 || o2.TotalQty != 4 || o2.Status != StatusPending {
		t.Fatalf("hidden order query wrong: %+v", o2)
	}
}
