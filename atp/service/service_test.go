package service_test

import (
	"testing"

	"ontology/atp/clock"
	"ontology/atp/order"
	"ontology/atp/reject"
	"ontology/atp/service"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustReject(t *testing.T, err error, r reject.Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected rejection %s, got success", r)
	}
	if !reject.Is(err, r) {
		t.Fatalf("expected rejection %s, got %v", r, err)
	}
}

func mustATP(t *testing.T, s *service.Service, wh, sku string, at int64, want int64) {
	t.Helper()
	got, err := s.QueryATP(wh, sku, clock.Time(at))
	must(t, err)
	if got != want {
		t.Fatalf("ATP(%s,%s,@%d) = %d, want %d", wh, sku, at, got, want)
	}
}

func newBasic(t *testing.T) *service.Service {
	t.Helper()
	s := service.New()
	must(t, s.AddWarehouse(0, "w1", 1))
	must(t, s.AddWarehouse(0, "w2", 2))
	return s
}

func commitReq(id string, now int64, lines []order.Line, split bool, ttl int64, maxWh int) order.Request {
	return order.Request{
		OrderID:       id,
		Lines:         lines,
		Now:           clock.Time(now),
		AllowSplit:    split,
		TTL:           ttl,
		MaxWarehouses: maxWh,
	}
}

// 预留恰到到期时刻即失效；差一秒仍有效。
func TestReservationExpiryBoundary(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(0, "w1", "skuA", 10))
	// t=10 承诺 6，ttl=100，到期时刻 110。
	res, err := s.Commit(commitReq("o1", 10, []order.Line{{SKU: "skuA", Qty: 6}}, false, 100, 1))
	must(t, err)
	if res.Expiry != 110 {
		t.Fatalf("expiry = %d, want 110", res.Expiry)
	}
	// 差一秒（t=109）：预留仍有效，可承诺量为 10-6=4。
	must(t, s.AddStock(109, "w2", "skuB", 1)) // 推进时钟到 109
	mustATP(t, s, "w1", "skuA", 109, 4)
	// 恰到到期时刻（t=110)：预留失效，可承诺量恢复为 10。
	must(t, s.AddStock(110, "w2", "skuB", 1))
	mustATP(t, s, "w1", "skuA", 110, 10)
	// 到期后确认出库报预留已过期；释放报订单不存在。
	mustReject(t, s.ConfirmOutbound(110, "o1"), reject.ReservationExpired)
	mustReject(t, s.Release(110, "o1"), reject.OrderNotFound)
	// 到期后同一订单号可再次承诺（不算重复）。
	_, err = s.Commit(commitReq("o1", 110, []order.Line{{SKU: "skuA", Qty: 1}}, false, 10, 1))
	must(t, err)
}

// 到期前一刻确认出库成功；确认后订单不再存在。
func TestConfirmOutboundBeforeExpiry(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(0, "w1", "skuA", 10))
	_, err := s.Commit(commitReq("o1", 0, []order.Line{{SKU: "skuA", Qty: 6}}, false, 100, 1))
	must(t, err)
	must(t, s.ConfirmOutbound(99, "o1"))
	mustATP(t, s, "w1", "skuA", 99, 4)
	mustReject(t, s.ConfirmOutbound(99, "o1"), reject.OrderNotFound)
	mustReject(t, s.Release(99, "o1"), reject.OrderNotFound)
}

// 计划入库到货时刻恰等于承诺时刻时计入可承诺量。
func TestInboundArrivalEqualsCommitTime(t *testing.T) {
	s := newBasic(t)
	must(t, s.ScheduleInbound(0, "w1", "skuA", "in1", 50, 8))
	// 到货前一刻不可承诺。
	mustATP(t, s, "w1", "skuA", 49, 0)
	// 恰等于到货时刻可承诺。
	must(t, s.AddStock(50, "w2", "skuB", 1)) // 推进时钟到 50
	mustATP(t, s, "w1", "skuA", 50, 8)
	res, err := s.Commit(commitReq("o1", 50, []order.Line{{SKU: "skuA", Qty: 8}}, false, 10, 1))
	must(t, err)
	if len(res.Allocations) != 1 || res.Allocations[0].Warehouse != "w1" || res.Allocations[0].Qty != 8 {
		t.Fatalf("unexpected allocations: %+v", res.Allocations)
	}
}

// 到货确认不重复计入；未到到货时刻不得确认；重复确认报参数非法。
func TestInboundConfirmNoDoubleCount(t *testing.T) {
	s := newBasic(t)
	must(t, s.ScheduleInbound(0, "w1", "skuA", "in1", 50, 10))
	// 未到到货时刻确认：参数非法，状态不变。
	mustReject(t, s.ConfirmInbound(49, "w1", "skuA", "in1"), reject.InvalidParam)
	mustATP(t, s, "w1", "skuA", 49, 0)
	// 到到货时刻确认：转为现货。
	must(t, s.ConfirmInbound(50, "w1", "skuA", "in1"))
	mustATP(t, s, "w1", "skuA", 50, 10)
	// 再次确认：参数非法，且不会重复计入。
	mustReject(t, s.ConfirmInbound(50, "w1", "skuA", "in1"), reject.InvalidParam)
	mustATP(t, s, "w1", "skuA", 50, 10)
}

// 永久缺货：全部仓库现货加全部计划入库之和即不足。
func TestPermanentStockout(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(0, "w1", "skuA", 3))
	must(t, s.ScheduleInbound(0, "w2", "skuA", "in1", 1000, 2))
	// 总供给 3+2=5 < 6，永久缺货。
	_, err := s.Commit(commitReq("o1", 0, []order.Line{{SKU: "skuA", Qty: 6}}, true, 10, 2))
	mustReject(t, err, reject.PermanentStockout)
	if reject.LineOf(err) != 0 {
		t.Fatalf("failing line = %d, want 0", reject.LineOf(err))
	}
	// 被拒绝的订单不改变任何状态。
	mustATP(t, s, "w1", "skuA", 0, 3)
	if _, qerr := s.QueryOrder("o1"); !reject.Is(qerr, reject.OrderNotFound) {
		t.Fatalf("rejected order must not exist, got %v", qerr)
	}
}

// 暂时缺货：总量足够，但受预留或到货时刻限制不足。
func TestTemporaryStockout(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(0, "w1", "skuA", 10))
	// 情形一：受预留限制。o1 预留 8，剩余 2 < 5，但总量 10 >= 5。
	_, err := s.Commit(commitReq("o1", 0, []order.Line{{SKU: "skuA", Qty: 8}}, false, 100, 1))
	must(t, err)
	_, err = s.Commit(commitReq("o2", 0, []order.Line{{SKU: "skuA", Qty: 5}}, false, 100, 1))
	mustReject(t, err, reject.TemporaryStockout)
	// 情形二：受到货时刻限制。skuB 现货 0，计划入库 5 于 t=100 到货。
	must(t, s.ScheduleInbound(0, "w1", "skuB", "inB", 100, 5))
	_, err = s.Commit(commitReq("o3", 0, []order.Line{{SKU: "skuB", Qty: 5}}, false, 100, 1))
	mustReject(t, err, reject.TemporaryStockout)
}

// 多行同时缺货：永久缺货优先于暂时缺货；同类报下标最小行。
func TestStockoutPriorityAcrossLines(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(0, "w1", "skuA", 10))
	must(t, s.AddStock(0, "w1", "skuB", 2))
	// o1 把 skuA 全部预留。
	_, err := s.Commit(commitReq("o1", 0, []order.Line{{SKU: "skuA", Qty: 10}}, false, 100, 1))
	must(t, err)
	// line0 skuA 缺 1：总量 10 足够但被预留 → 暂时缺货；
	// line1 skuB 缺 5：总量 2 不足 → 永久缺货。永久优先，报 line1。
	_, err = s.Commit(commitReq("o2", 0, []order.Line{
		{SKU: "skuA", Qty: 1},
		{SKU: "skuB", Qty: 5},
	}, true, 100, 2))
	mustReject(t, err, reject.PermanentStockout)
	if reject.LineOf(err) != 1 {
		t.Fatalf("failing line = %d, want 1", reject.LineOf(err))
	}
	// 两行皆暂时缺货时报下标最小行。
	_, err = s.Commit(commitReq("o3", 0, []order.Line{
		{SKU: "skuA", Qty: 1},
		{SKU: "skuA", Qty: 2},
	}, false, 100, 1))
	mustReject(t, err, reject.TemporaryStockout)
	if reject.LineOf(err) != 0 {
		t.Fatalf("failing line = %d, want 0", reject.LineOf(err))
	}
}

// 拆分仓库数恰等于上限成功，超一拒绝。
func TestSplitWarehouseLimit(t *testing.T) {
	s := service.New()
	must(t, s.AddWarehouse(0, "w1", 1))
	must(t, s.AddWarehouse(0, "w2", 2))
	must(t, s.AddWarehouse(0, "w3", 3))
	must(t, s.AddStock(0, "w1", "skuA", 4))
	must(t, s.AddStock(0, "w2", "skuA", 4))
	must(t, s.AddStock(0, "w3", "skuA", 4))
	// 需要 9 = 4+4+1，恰用 3 仓，上限 3：成功。
	res, err := s.Commit(commitReq("o1", 0, []order.Line{{SKU: "skuA", Qty: 9}}, true, 100, 3))
	must(t, err)
	if len(res.Allocations) != 3 {
		t.Fatalf("allocations = %+v, want 3 segments", res.Allocations)
	}
	want := []order.Allocation{
		{Line: 0, Warehouse: "w1", SKU: "skuA", Qty: 4},
		{Line: 0, Warehouse: "w2", SKU: "skuA", Qty: 4},
		{Line: 0, Warehouse: "w3", SKU: "skuA", Qty: 1},
	}
	for i, a := range res.Allocations {
		if a != want[i] {
			t.Fatalf("allocation[%d] = %+v, want %+v", i, a, want[i])
		}
	}
	// 上限 2：单行需 3 仓才能满足，拒绝拆分过多。
	must(t, s.AddStock(0, "w1", "skuC", 4))
	must(t, s.AddStock(0, "w2", "skuC", 4))
	must(t, s.AddStock(0, "w3", "skuC", 4))
	_, err = s.Commit(commitReq("o4", 0, []order.Line{{SKU: "skuC", Qty: 9}}, true, 100, 2))
	mustReject(t, err, reject.TooManySplits)
	// 被拒绝后状态不变。
	mustATP(t, s, "w1", "skuC", 0, 4)
}

// 不允许拆分时单行须由单一仓库全量满足，选优先序号最小者。
func TestNoSplitSingleWarehouse(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(0, "w1", "skuA", 3))
	must(t, s.AddStock(0, "w2", "skuA", 10))
	// w1 不足，选 w2。
	res, err := s.Commit(commitReq("o1", 0, []order.Line{{SKU: "skuA", Qty: 5}}, false, 100, 1))
	must(t, err)
	if len(res.Allocations) != 1 || res.Allocations[0].Warehouse != "w2" {
		t.Fatalf("allocations = %+v, want single w2 segment", res.Allocations)
	}
	// 再要 3：w1 恰有 3，选优先级更小的 w1。
	res, err = s.Commit(commitReq("o2", 0, []order.Line{{SKU: "skuA", Qty: 3}}, false, 100, 1))
	must(t, err)
	if res.Allocations[0].Warehouse != "w1" {
		t.Fatalf("allocations = %+v, want w1", res.Allocations)
	}
}

// 同订单多行互相影响：后面的行看到前面行已占用后的可承诺量。
func TestMultiLineInteraction(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(0, "w1", "skuA", 10))
	// line0 占 6 后仅剩 4，line1 要 6：总供给 10-6=4 < 6 → 永久缺货 line1。
	_, err := s.Commit(commitReq("o1", 0, []order.Line{
		{SKU: "skuA", Qty: 6},
		{SKU: "skuA", Qty: 6},
	}, false, 100, 1))
	mustReject(t, err, reject.PermanentStockout)
	if reject.LineOf(err) != 1 {
		t.Fatalf("failing line = %d, want 1", reject.LineOf(err))
	}
	// 整单全有或全无：line0 的占用不得落账。
	mustATP(t, s, "w1", "skuA", 0, 10)
	// 有未来入库时同情形变为暂时缺货。
	must(t, s.ScheduleInbound(0, "w1", "skuA", "in1", 1000, 10))
	_, err = s.Commit(commitReq("o2", 0, []order.Line{
		{SKU: "skuA", Qty: 6},
		{SKU: "skuA", Qty: 6},
	}, false, 100, 1))
	mustReject(t, err, reject.TemporaryStockout)
	// 两行各要 5：成功，且都落在 w1。
	res, err := s.Commit(commitReq("o3", 0, []order.Line{
		{SKU: "skuA", Qty: 5},
		{SKU: "skuA", Qty: 5},
	}, false, 100, 1))
	must(t, err)
	if len(res.Allocations) != 2 || res.Allocations[0].Warehouse != "w1" || res.Allocations[1].Warehouse != "w1" {
		t.Fatalf("allocations = %+v", res.Allocations)
	}
	mustATP(t, s, "w1", "skuA", 0, 0)
}

// 时钟回退：操作时刻小于上一次被接受操作的时刻即拒绝；
// 被拒绝的操作不改变任何状态与时钟。
func TestClockRollback(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(10, "w1", "skuA", 10))
	if s.Now() != 10 {
		t.Fatalf("now = %d, want 10", s.Now())
	}
	// t=9 的操作报时钟回退。
	mustReject(t, s.AddStock(9, "w1", "skuA", 5), reject.ClockRollback)
	_, err := s.Commit(commitReq("o1", 9, []order.Line{{SKU: "skuA", Qty: 1}}, false, 10, 1))
	mustReject(t, err, reject.ClockRollback)
	mustReject(t, s.ConfirmOutbound(9, "o1"), reject.ClockRollback)
	// 时钟与状态均未改变。
	if s.Now() != 10 {
		t.Fatalf("now = %d, want 10 after rejected ops", s.Now())
	}
	mustATP(t, s, "w1", "skuA", 10, 10)
	// 等于当前时刻的操作允许。
	must(t, s.AddStock(10, "w1", "skuA", 5))
	mustATP(t, s, "w1", "skuA", 10, 15)
}

// 同一订单号已存在有效预留时再次承诺报订单重复；
// 释放后可再次承诺。
func TestDuplicateOrder(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(0, "w1", "skuA", 10))
	_, err := s.Commit(commitReq("o1", 0, []order.Line{{SKU: "skuA", Qty: 5}}, false, 100, 1))
	must(t, err)
	_, err = s.Commit(commitReq("o1", 1, []order.Line{{SKU: "skuA", Qty: 1}}, false, 100, 1))
	mustReject(t, err, reject.DuplicateOrder)
	// 释放后不再重复。
	must(t, s.Release(2, "o1"))
	_, err = s.Commit(commitReq("o1", 2, []order.Line{{SKU: "skuA", Qty: 1}}, false, 100, 1))
	must(t, err)
}

// 释放与确认的错误可区分于订单号重复；不存在的订单报订单不存在。
func TestReleaseConfirmErrors(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(0, "w1", "skuA", 10))
	mustReject(t, s.Release(0, "ghost"), reject.OrderNotFound)
	mustReject(t, s.ConfirmOutbound(0, "ghost"), reject.OrderNotFound)
	// 释放有效预留成功，量立即归还。
	_, err := s.Commit(commitReq("o1", 0, []order.Line{{SKU: "skuA", Qty: 6}}, false, 100, 1))
	must(t, err)
	mustATP(t, s, "w1", "skuA", 0, 4)
	must(t, s.Release(0, "o1"))
	mustATP(t, s, "w1", "skuA", 0, 10)
}

// 拒绝优先级：参数非法 > 时钟回退 > 订单重复 > 永久缺货 > 暂时缺货 > 拆分过多。
func TestRejectionPriority(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(10, "w1", "skuA", 10))
	// 参数非法优先于时钟回退：数量为负且时刻回退，报参数非法。
	mustReject(t, s.AddStock(5, "w1", "skuA", -1), reject.InvalidParam)
	badReq := commitReq("oX", 5, []order.Line{{SKU: "skuA", Qty: 0}}, false, 10, 1)
	_, err := s.Commit(badReq)
	mustReject(t, err, reject.InvalidParam)
	// 时钟回退优先于订单重复：o1 在 t=10 成功，t=9 的重复承诺报时钟回退。
	_, err = s.Commit(commitReq("o1", 10, []order.Line{{SKU: "skuA", Qty: 8}}, false, 100, 1))
	must(t, err)
	_, err = s.Commit(commitReq("o1", 9, []order.Line{{SKU: "skuA", Qty: 1}}, false, 100, 1))
	mustReject(t, err, reject.ClockRollback)
	// 订单重复优先于缺货：o1 重复且数量远超供给，报订单重复。
	_, err = s.Commit(commitReq("o1", 10, []order.Line{{SKU: "skuA", Qty: 999}}, false, 100, 1))
	mustReject(t, err, reject.DuplicateOrder)
	// 永久缺货优先于暂时缺货（skuB 总量不足永久，skuA 被预留暂时）。
	must(t, s.AddStock(10, "w1", "skuB", 1))
	_, err = s.Commit(commitReq("o2", 10, []order.Line{
		{SKU: "skuA", Qty: 5},
		{SKU: "skuB", Qty: 2},
	}, true, 100, 2))
	mustReject(t, err, reject.PermanentStockout)
	// 暂时缺货优先于拆分过多：skuA 剩余 2，要 5 且上限 1，报暂时缺货。
	_, err = s.Commit(commitReq("o3", 10, []order.Line{{SKU: "skuA", Qty: 5}}, true, 100, 1))
	mustReject(t, err, reject.TemporaryStockout)
}

// 查询只读：不推进时钟；查询时刻早于当前时刻报参数非法。
func TestQueryRules(t *testing.T) {
	s := newBasic(t)
	must(t, s.AddStock(10, "w1", "skuA", 10))
	_, err := s.Commit(commitReq("o1", 10, []order.Line{{SKU: "skuA", Qty: 4}}, false, 100, 1))
	must(t, err)
	// 查询未来时刻：计划入库到货时刻不晚于查询时刻即计入。
	must(t, s.ScheduleInbound(10, "w1", "skuA", "in1", 50, 7))
	mustATP(t, s, "w1", "skuA", 60, 13) // 10 - 4 + 7
	mustATP(t, s, "w1", "skuA", 10, 6)
	// 查询不推进时钟。
	if s.Now() != 10 {
		t.Fatalf("query advanced clock to %d", s.Now())
	}
	// 查询时刻早于当前时刻：参数非法。
	_, qerr := s.QueryATP("w1", "skuA", 9)
	mustReject(t, qerr, reject.InvalidParam)
	// 查询不存在的仓库：参数非法。
	_, qerr = s.QueryATP("wX", "skuA", 10)
	mustReject(t, qerr, reject.InvalidParam)
	// 订单明细查询。
	d, qerr := s.QueryOrder("o1")
	must(t, qerr)
	if !d.Valid || d.Expiry != 110 || len(d.Lines) != 1 || d.Lines[0].Warehouse != "w1" || d.Lines[0].Qty != 4 {
		t.Fatalf("order detail = %+v", d)
	}
	// 推进时钟到到期后，明细仍在但标记失效。
	must(t, s.AddStock(110, "w2", "skuB", 1))
	d, qerr = s.QueryOrder("o1")
	must(t, qerr)
	if d.Valid {
		t.Fatalf("expired order should be invalid: %+v", d)
	}
	_, qerr = s.QueryOrder("ghost")
	mustReject(t, qerr, reject.OrderNotFound)
}

// 相同操作序列重放得到完全相同的发货仓分配。
func TestDeterministicReplay(t *testing.T) {
	run := func() []*order.Result {
		s := service.New()
		must(t, s.AddWarehouse(0, "w1", 1))
		must(t, s.AddWarehouse(0, "w2", 2))
		must(t, s.AddWarehouse(0, "w3", 3))
		must(t, s.AddStock(0, "w1", "skuA", 5))
		must(t, s.AddStock(0, "w2", "skuA", 5))
		must(t, s.AddStock(0, "w3", "skuA", 5))
		must(t, s.ScheduleInbound(1, "w2", "skuA", "in1", 10, 5))
		var results []*order.Result
		for i, req := range []order.Request{
			commitReq("o1", 2, []order.Line{{SKU: "skuA", Qty: 7}}, true, 50, 3),
			commitReq("o2", 3, []order.Line{{SKU: "skuA", Qty: 4}}, false, 50, 1),
			commitReq("o3", 11, []order.Line{{SKU: "skuA", Qty: 6}}, true, 50, 2),
		} {
			res, err := s.Commit(req)
			if err != nil {
				t.Fatalf("run op %d: %v", i, err)
			}
			results = append(results, res)
		}
		return results
	}
	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("replay length mismatch")
	}
	for i := range first {
		if first[i].Expiry != second[i].Expiry || len(first[i].Allocations) != len(second[i].Allocations) {
			t.Fatalf("replay mismatch at %d", i)
		}
		for j := range first[i].Allocations {
			if first[i].Allocations[j] != second[i].Allocations[j] {
				t.Fatalf("replay mismatch at %d/%d: %+v vs %+v", i, j, first[i].Allocations[j], second[i].Allocations[j])
			}
		}
	}
}
