package inventory

import (
	"reflect"
	"testing"
)

// newTestSystem 创建带三个仓库（优先序号 1、2、3）的系统。
func newTestSystem(t *testing.T) *System {
	t.Helper()
	s := NewSystem()
	for i, id := range []string{"W1", "W2", "W3"} {
		if err := s.AddWarehouse(id, i+1); err != nil {
			t.Fatalf("AddWarehouse(%s) 失败: %v", id, err)
		}
	}
	return s
}

// mustOK 断言操作被接受。
func mustOK(t *testing.T, err *Error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际被拒绝: %v", err)
	}
}

// mustReject 断言操作以指定原因被拒绝。
func mustReject(t *testing.T, err *Error, reason Reason) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望被拒绝(%s)，实际成功", reason)
	}
	if err.Reason != reason {
		t.Fatalf("期望拒绝原因 %s，实际 %s（%v）", reason, err.Reason, err)
	}
}

func mustATP(t *testing.T, s *System, wh, product string, at, want int64) {
	t.Helper()
	got, err := s.ATP(wh, product, at)
	mustOK(t, err)
	if got != want {
		t.Fatalf("ATP(%s,%s,%d) = %d，期望 %d", wh, product, at, got, want)
	}
}

// TestReservationExpiryBoundary 预留恰到到期即失效、差一秒仍有效。
func TestReservationExpiryBoundary(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 10, 0))
	res, err := s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 4}}, 0, false, 5, 1)
	mustOK(t, err)
	if res.ExpireAt != 5 {
		t.Fatalf("到期时刻 = %d，期望 5", res.ExpireAt)
	}

	// 差一秒（当前时刻 4 < 到期 5）：预留仍有效，占用 4。
	mustOK(t, s.AddOnHand("W1", "P", 1, 4))
	mustATP(t, s, "W1", "P", 4, 11-4)
	details, err := s.ReservationDetails("O1")
	mustOK(t, err)
	if len(details) != 1 || !details[0].Valid {
		t.Fatalf("时刻 4 预留应仍有效: %+v", details)
	}

	// 恰到到期（当前时刻 5 == 到期 5）：预留失效，不再占用任何量。
	mustOK(t, s.AddOnHand("W1", "P", 1, 5))
	mustATP(t, s, "W1", "P", 5, 12)
	mustATP(t, s, "W1", "P", 100, 12)
	details, err = s.ReservationDetails("O1")
	mustOK(t, err)
	if len(details) != 1 || details[0].Valid {
		t.Fatalf("时刻 5 预留应已失效: %+v", details)
	}
	mustOK(t, s.Validate())
}

// TestDuplicateOrder 同一订单号已存在有效预留时再次承诺报订单重复；
// 预留到期后同一订单号可再次承诺（与订单不存在/预留已过期可区分）。
func TestDuplicateOrder(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 10, 0))
	_, err := s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 4}}, 0, false, 5, 1)
	mustOK(t, err)

	// 预留仍有效（时刻 1 < 到期 5）：订单重复。
	_, err = s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 1}}, 1, false, 5, 1)
	mustReject(t, err, ReasonDuplicateOrder)

	// 预留到期后（时刻 5 == 到期 5）：允许再次承诺，覆盖旧记录。
	mustOK(t, s.AddOnHand("W1", "P", 1, 5))
	_, err = s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 2}}, 5, false, 5, 1)
	mustOK(t, err)
	mustATP(t, s, "W1", "P", 5, 11-2)
	mustOK(t, s.Validate())
}

// TestConfirmOutboundAndRelease 确认出库与释放预留的全部分支。
func TestConfirmOutboundAndRelease(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 10, 0))

	// 有效预留确认出库：转为现货扣减，预留消失。
	_, err := s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 4}}, 0, false, 5, 1)
	mustOK(t, err)
	mustOK(t, s.ConfirmOutbound("O1", 3))
	mustATP(t, s, "W1", "P", 3, 6)
	// 已出库的订单不存在。
	mustReject(t, s.ConfirmOutbound("O1", 3), ReasonOrderNotFound)
	mustReject(t, s.ReleaseReservation("O1", 3), ReasonOrderNotFound)

	// 已到期的预留不可确认：报预留已过期（区别于订单不存在）。
	_, err = s.CommitOrder("O2", []OrderLine{{Product: "P", Qty: 2}}, 3, false, 2, 1)
	mustOK(t, err)
	mustOK(t, s.AddOnHand("W1", "P", 1, 5))
	mustReject(t, s.ConfirmOutbound("O2", 5), ReasonReservationExpired)
	// 释放已到期的预留：报订单不存在（与预留已过期可区分）。
	mustReject(t, s.ReleaseReservation("O2", 5), ReasonOrderNotFound)
	// 到期预留不再占用量。
	mustATP(t, s, "W1", "P", 5, 7)

	// 释放仍有效的预留：量被归还。
	_, err = s.CommitOrder("O3", []OrderLine{{Product: "P", Qty: 3}}, 5, false, 10, 1)
	mustOK(t, err)
	mustATP(t, s, "W1", "P", 5, 4)
	mustOK(t, s.ReleaseReservation("O3", 6))
	mustATP(t, s, "W1", "P", 6, 7)
	mustReject(t, s.ReleaseReservation("O3", 6), ReasonOrderNotFound)

	// 释放/确认不存在的订单：订单不存在。
	mustReject(t, s.ReleaseReservation("O-X", 6), ReasonOrderNotFound)
	mustReject(t, s.ConfirmOutbound("O-X", 6), ReasonOrderNotFound)
	mustOK(t, s.Validate())
}

// TestConfirmOutboundInsufficientOnHand 预留由未来到货支撑时，
// 确认出库不得使现货为负：先报现货不足，到货确认后可正常出库。
func TestConfirmOutboundInsufficientOnHand(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AddPlannedInbound("W1", "P", "I1", 100, 10, 0))
	_, err := s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 10}}, 100, false, 200, 1)
	mustOK(t, err)

	// 现货 0 < 预留 10：拒绝，现货不得为负。
	mustReject(t, s.ConfirmOutbound("O1", 150), ReasonInsufficientOnHand)
	mustATP(t, s, "W1", "P", 150, 0)

	// 到货确认转为现货后可正常出库。
	mustOK(t, s.ConfirmArrival("W1", "P", "I1", 160))
	mustOK(t, s.ConfirmOutbound("O1", 160))
	mustATP(t, s, "W1", "P", 160, 0)
	mustOK(t, s.Validate())
}

// TestAllOrNothing 任一行不满足则整单无变化。
func TestAllOrNothing(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 2, 0))
	_, err := s.CommitOrder("O1", []OrderLine{
		{Product: "P", Qty: 2},
		{Product: "P", Qty: 100},
	}, 0, false, 10, 1)
	mustReject(t, err, ReasonPermanentShortage)

	// 行 0 未留下任何占用，订单未登记，可同名再次承诺。
	mustATP(t, s, "W1", "P", 0, 2)
	if _, derr := s.ReservationDetails("O1"); derr == nil || derr.Reason != ReasonOrderNotFound {
		t.Fatalf("被拒绝的订单不应留下预留: %v", derr)
	}
	_, err = s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 2}}, 0, false, 10, 1)
	mustOK(t, err)
	mustOK(t, s.Validate())
}

// TestQueryClockRules 查询只读、不推进时钟；查询时刻早于当前时刻报参数非法。
func TestQueryClockRules(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 10, 5))

	// 查询时刻早于当前时刻：参数非法。
	_, err := s.ATP("W1", "P", 4)
	mustReject(t, err, ReasonInvalidParam)
	// 承诺时刻早于当前时刻：时钟回退（与查询的参数非法相区分）。
	_, cerr := s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 1}}, 4, false, 10, 1)
	mustReject(t, cerr, ReasonClockRollback)

	// 查询不推进时钟：查询时刻 100 之后，时钟仍为 5。
	mustATP(t, s, "W1", "P", 100, 10)
	if _, err := s.ReservationDetails("OX"); err == nil {
		t.Fatalf("不存在的订单应报订单不存在")
	}
	if got := s.Clock(); got != 5 {
		t.Fatalf("查询推进了时钟: %d", got)
	}
	_, cerr = s.CommitOrder("O2", []OrderLine{{Product: "P", Qty: 1}}, 5, false, 10, 1)
	mustOK(t, cerr)

	// 未注册仓库查询：参数非法。
	_, err = s.ATP("W-X", "P", 5)
	mustReject(t, err, ReasonInvalidParam)
	mustOK(t, s.Validate())
}

// TestRejectionPriority 拒绝优先级：参数非法 > 时钟回退 > 订单重复 > 缺货 > 拆分过多。
func TestRejectionPriority(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 10, 5))
	_, err := s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 8}}, 5, false, 100, 1)
	mustOK(t, err)

	// 参数非法优先于时钟回退：空订单号 + 时刻回退 → 参数非法。
	_, cerr := s.CommitOrder("", []OrderLine{{Product: "P", Qty: 1}}, 4, false, 10, 1)
	mustReject(t, cerr, ReasonInvalidParam)

	// 时钟回退优先于订单重复：有效预留订单 + 时刻回退 → 时钟回退。
	_, cerr = s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 1}}, 4, false, 10, 1)
	mustReject(t, cerr, ReasonClockRollback)

	// 订单重复优先于缺货：有效预留订单 + 超大数量 → 订单重复。
	_, cerr = s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 99999}}, 5, true, 10, 3)
	mustReject(t, cerr, ReasonDuplicateOrder)

	// 缺货优先于拆分过多：数量远超总供给 + 上限 1 → 永久缺货（而非拆分过多）。
	_, cerr = s.CommitOrder("O2", []OrderLine{{Product: "P", Qty: 99999}}, 5, true, 10, 1)
	mustReject(t, cerr, ReasonPermanentShortage)

	// 暂时缺货优先于拆分过多：总供给够但被预留占住 + 上限 1 → 暂时缺货。
	_, cerr = s.CommitOrder("O3", []OrderLine{{Product: "P", Qty: 5}}, 5, true, 10, 1)
	mustReject(t, cerr, ReasonTemporaryShortage)
	mustOK(t, s.Validate())
}

// TestReservationExaminationBounded 以可验证的方式证明：
// 单行承诺判定考察的预留记录数不随历史订单总数或已失效预留数增长。
func TestReservationExaminationBounded(t *testing.T) {
	examinedAfterWarmup := func(history int) int64 {
		s := newTestSystem(t)
		mustOK(t, s.AddOnHand("W1", "P", int64(history)*2+10, 0))
		// 制造 history 条历史预留，全部在时刻 1 到期。
		for i := 0; i < history; i++ {
			_, err := s.CommitOrder("H"+itoa(i), []OrderLine{{Product: "P", Qty: 1}}, 0, false, 1, 1)
			mustOK(t, err)
		}
		// 推进时钟越过全部到期时刻；第一次 ATP 触发惰性清理（摊还的一次性代价）。
		mustOK(t, s.AddOnHand("W1", "P", 1, 10))
		mustATP(t, s, "W1", "P", 10, int64(history)*2+11)
		s.ResetStats()
		// 此后单次承诺判定考察的预留记录数应与 history 无关。
		_, err := s.CommitOrder("X", []OrderLine{{Product: "P", Qty: 1}}, 10, false, 5, 1)
		mustOK(t, err)
		return s.Stats().ReservationsExamined
	}

	small := examinedAfterWarmup(1000)
	large := examinedAfterWarmup(4000)
	t.Logf("历史 1000 单后单次承诺考察预留数 = %d；历史 4000 单后 = %d", small, large)
	if small > 1 || large > 1 {
		t.Fatalf("单次承诺考察的预留记录数随历史增长: %d vs %d", small, large)
	}
	if small != large {
		t.Fatalf("考察次数不应随历史订单数变化: %d != %d", small, large)
	}
}

// TestInboundArrivalEqualsCommitTime 到货时刻恰等于承诺时刻时计入可承诺量。
func TestInboundArrivalEqualsCommitTime(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AddPlannedInbound("W1", "P", "I1", 10, 5, 0))

	mustATP(t, s, "W1", "P", 9, 0)
	mustATP(t, s, "W1", "P", 10, 5)

	// 承诺时刻 9 < 到货 10：不足（暂时缺货，总供给是够的）。
	_, err := s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 5}}, 9, false, 10, 1)
	mustReject(t, err, ReasonTemporaryShortage)

	// 承诺时刻 10 == 到货 10：计入，承诺成功。
	res, err := s.CommitOrder("O2", []OrderLine{{Product: "P", Qty: 5}}, 10, false, 10, 1)
	mustOK(t, err)
	want := []Allocation{{LineIndex: 0, Warehouse: "W1", Product: "P", Qty: 5}}
	if !reflect.DeepEqual(res.Allocations, want) {
		t.Fatalf("分配 = %+v，期望 %+v", res.Allocations, want)
	}
	mustOK(t, s.Validate())
}

// TestPermanentVsTemporaryShortage 永久缺货与暂时缺货的区分，
// 以及多行同时缺货时报下标最小的缺货行。
func TestPermanentVsTemporaryShortage(t *testing.T) {
	// 永久缺货：所有仓库现货加全部计划入库总和本身不足。
	s := newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 3, 0))
	_, err := s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 10}}, 0, true, 10, 3)
	mustReject(t, err, ReasonPermanentShortage)
	if err.Line != 0 {
		t.Fatalf("缺货行下标 = %d，期望 0", err.Line)
	}

	// 暂时缺货（受预留限制）：总供给 10 足够，但 8 被有效预留占用。
	s = newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 10, 0))
	_, err = s.CommitOrder("A", []OrderLine{{Product: "P", Qty: 8}}, 0, false, 100, 1)
	mustOK(t, err)
	_, err = s.CommitOrder("B", []OrderLine{{Product: "P", Qty: 5}}, 0, true, 10, 3)
	mustReject(t, err, ReasonTemporaryShortage)

	// 暂时缺货（受到货时刻限制）：总供给 10 足够，但到货时刻晚于承诺时刻。
	s = newTestSystem(t)
	mustOK(t, s.AddPlannedInbound("W1", "P", "I1", 100, 10, 0))
	_, err = s.CommitOrder("C", []OrderLine{{Product: "P", Qty: 5}}, 10, true, 10, 3)
	mustReject(t, err, ReasonTemporaryShortage)

	// 多行缺货报下标最小者：行 0 可行，行 1 永久缺货（总供给 3，行 0 已占 2）。
	s = newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 3, 0))
	_, err = s.CommitOrder("D", []OrderLine{
		{Product: "P", Qty: 2},
		{Product: "P", Qty: 10},
	}, 0, false, 10, 3)
	mustReject(t, err, ReasonPermanentShortage)
	if err.Line != 1 {
		t.Fatalf("缺货行下标 = %d，期望 1", err.Line)
	}

	// 多行缺货报下标最小者：行 1 暂时缺货（总供给 10 够 5，但被预留占 9）。
	s = newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 10, 0))
	_, err = s.CommitOrder("E0", []OrderLine{{Product: "P", Qty: 9}}, 0, false, 100, 1)
	mustOK(t, err)
	_, err = s.CommitOrder("E1", []OrderLine{
		{Product: "P", Qty: 1},
		{Product: "P", Qty: 5},
	}, 0, true, 10, 3)
	mustReject(t, err, ReasonTemporaryShortage)
	if err.Line != 1 {
		t.Fatalf("缺货行下标 = %d，期望 1", err.Line)
	}
	mustOK(t, s.Validate())
}

// TestSplitWarehouseLimit 拆分仓库数恰等于上限成功、超一拒绝；
// 且不为凑够上限而改用其他取用方案。
func TestSplitWarehouseLimit(t *testing.T) {
	setup := func() *System {
		s := newTestSystem(t)
		mustOK(t, s.AddOnHand("W1", "P", 3, 0))
		mustOK(t, s.AddOnHand("W2", "P", 3, 0))
		mustOK(t, s.AddOnHand("W3", "P", 3, 0))
		return s
	}

	// 恰等于上限（3 个仓库）：成功，按优先序贪心取用。
	s := setup()
	res, err := s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 7}}, 0, true, 10, 3)
	mustOK(t, err)
	want := []Allocation{
		{LineIndex: 0, Warehouse: "W1", Product: "P", Qty: 3},
		{LineIndex: 0, Warehouse: "W2", Product: "P", Qty: 3},
		{LineIndex: 0, Warehouse: "W3", Product: "P", Qty: 1},
	}
	if !reflect.DeepEqual(res.Allocations, want) {
		t.Fatalf("分配 = %+v，期望 %+v", res.Allocations, want)
	}

	// 超一（上限 2，贪心需 3 个仓库）：拒绝，且状态不变。
	s = setup()
	_, err = s.CommitOrder("O2", []OrderLine{{Product: "P", Qty: 7}}, 0, true, 10, 2)
	mustReject(t, err, ReasonTooManySplits)
	mustATP(t, s, "W1", "P", 0, 3)
	if _, err := s.ReservationDetails("O2"); err == nil || err.Reason != ReasonOrderNotFound {
		t.Fatalf("被拒绝的订单不应留下预留: %v", err)
	}

	// 不得为凑够上限而改用其他取用方案：
	// W3 一仓即可满足，但贪心先取 W1、W2（2 个仓库 > 上限 1），必须拒绝。
	s = newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 1, 0))
	mustOK(t, s.AddOnHand("W2", "P", 1, 0))
	mustOK(t, s.AddOnHand("W3", "P", 10, 0))
	_, err = s.CommitOrder("O3", []OrderLine{{Product: "P", Qty: 2}}, 0, true, 10, 1)
	mustReject(t, err, ReasonTooManySplits)

	// 不允许拆分时，整单涉及仓库数同样受上限约束。
	s = newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "A", 5, 0))
	mustOK(t, s.AddOnHand("W2", "B", 5, 0))
	_, err = s.CommitOrder("O4", []OrderLine{
		{Product: "A", Qty: 1},
		{Product: "B", Qty: 1},
	}, 0, false, 10, 1)
	mustReject(t, err, ReasonTooManySplits)
	mustOK(t, s.Validate())
}

// TestMultiLineInteraction 同订单多行互相影响：后面的行看到前面行已占用后的量。
func TestMultiLineInteraction(t *testing.T) {
	// 不允许拆分：行 0 占 W1 后，行 1 只能去 W2。
	s := newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 5, 0))
	mustOK(t, s.AddOnHand("W2", "P", 5, 0))
	res, err := s.CommitOrder("O1", []OrderLine{
		{Product: "P", Qty: 4},
		{Product: "P", Qty: 4},
	}, 0, false, 10, 2)
	mustOK(t, err)
	want := []Allocation{
		{LineIndex: 0, Warehouse: "W1", Product: "P", Qty: 4},
		{LineIndex: 1, Warehouse: "W2", Product: "P", Qty: 4},
	}
	if !reflect.DeepEqual(res.Allocations, want) {
		t.Fatalf("分配 = %+v，期望 %+v", res.Allocations, want)
	}

	// 允许拆分：行 0 取走 W1 全部与 W2 部分，行 1 只能看到剩余。
	s = newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 5, 0))
	mustOK(t, s.AddOnHand("W2", "P", 5, 0))
	res, err = s.CommitOrder("O2", []OrderLine{
		{Product: "P", Qty: 7},
		{Product: "P", Qty: 3},
	}, 0, true, 10, 2)
	mustOK(t, err)
	want = []Allocation{
		{LineIndex: 0, Warehouse: "W1", Product: "P", Qty: 5},
		{LineIndex: 0, Warehouse: "W2", Product: "P", Qty: 2},
		{LineIndex: 1, Warehouse: "W2", Product: "P", Qty: 3},
	}
	if !reflect.DeepEqual(res.Allocations, want) {
		t.Fatalf("分配 = %+v，期望 %+v", res.Allocations, want)
	}

	// 行 1 不足时分类要考虑行 0 的占用：总供给 10，行 0 占 7，剩 3 < 7，
	// 属永久缺货（即使忽略预留与到货时刻也不可能满足）。
	s = newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 5, 0))
	mustOK(t, s.AddOnHand("W2", "P", 5, 0))
	_, err = s.CommitOrder("O3", []OrderLine{
		{Product: "P", Qty: 7},
		{Product: "P", Qty: 7},
	}, 0, true, 10, 3)
	mustReject(t, err, ReasonPermanentShortage)
	if err.Line != 1 {
		t.Fatalf("缺货行下标 = %d，期望 1", err.Line)
	}
	mustOK(t, s.Validate())
}

// TestConfirmArrivalNoDoubleCount 到货确认不重复计入，且不得重复确认。
func TestConfirmArrivalNoDoubleCount(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AddPlannedInbound("W1", "P", "I1", 5, 10, 0))
	mustATP(t, s, "W1", "P", 4, 0)
	mustATP(t, s, "W1", "P", 5, 10)

	// 确认到货：转为现货，ATP 仍为 10（现货 10 + 计划 0），不重复计入。
	mustOK(t, s.ConfirmArrival("W1", "P", "I1", 6))
	mustATP(t, s, "W1", "P", 6, 10)
	mustATP(t, s, "W1", "P", 100, 10)

	// 重复确认 / 确认不存在的入库单：拒绝。
	mustReject(t, s.ConfirmArrival("W1", "P", "I1", 7), ReasonInboundNotFound)
	mustReject(t, s.ConfirmArrival("W1", "P", "I-X", 7), ReasonInboundNotFound)

	// 被拒绝的确认不改变状态。
	mustATP(t, s, "W1", "P", 7, 10)
	mustOK(t, s.Validate())
}

// TestNoSplitChoosesLowestPrioritySufficient 不允许拆分时，
// 选可承诺量足够的仓库中优先序号最小者。
func TestNoSplitChoosesLowestPrioritySufficient(t *testing.T) {
	// W1 不足（2 < 5），选 W2。
	s := newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 2, 0))
	mustOK(t, s.AddOnHand("W2", "P", 10, 0))
	res, err := s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 5}}, 0, false, 10, 1)
	mustOK(t, err)
	if len(res.Allocations) != 1 || res.Allocations[0].Warehouse != "W2" {
		t.Fatalf("应选 W2: %+v", res.Allocations)
	}

	// W1、W2 都足够，选优先序号最小的 W1。
	s = newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 10, 0))
	mustOK(t, s.AddOnHand("W2", "P", 10, 0))
	res, err = s.CommitOrder("O2", []OrderLine{{Product: "P", Qty: 5}}, 0, false, 10, 1)
	mustOK(t, err)
	if len(res.Allocations) != 1 || res.Allocations[0].Warehouse != "W1" {
		t.Fatalf("应选 W1: %+v", res.Allocations)
	}
	mustOK(t, s.Validate())
}

// TestClockRollbackAndAtomicity 时钟回退被拒绝，且被拒绝的操作不改变状态与时钟。
func TestClockRollbackAndAtomicity(t *testing.T) {
	s := newTestSystem(t)
	mustOK(t, s.AddOnHand("W1", "P", 10, 5))
	mustOK(t, s.AddPlannedInbound("W1", "P", "I1", 20, 5, 5))
	_, err := s.CommitOrder("O1", []OrderLine{{Product: "P", Qty: 3}}, 5, false, 50, 1)
	mustOK(t, err)

	// 各类操作携带时刻 4 < 当前时刻 5：一律时钟回退。
	mustReject(t, s.AddOnHand("W1", "P", 1, 4), ReasonClockRollback)
	mustReject(t, s.AddPlannedInbound("W1", "P", "I2", 30, 1, 4), ReasonClockRollback)
	mustReject(t, s.ConfirmArrival("W1", "P", "I1", 4), ReasonClockRollback)
	_, cerr := s.CommitOrder("O2", []OrderLine{{Product: "P", Qty: 1}}, 4, false, 10, 1)
	mustReject(t, cerr, ReasonClockRollback)
	mustReject(t, s.ConfirmOutbound("O1", 4), ReasonClockRollback)
	mustReject(t, s.ReleaseReservation("O1", 4), ReasonClockRollback)

	// 状态与时钟均未改变。
	if got := s.Clock(); got != 5 {
		t.Fatalf("时钟 = %d，期望 5", got)
	}
	mustATP(t, s, "W1", "P", 5, 10-3)

	// 业务拒绝同样不改变状态与时钟：缺货拒绝后 ATP 不变、订单未登记。
	_, cerr = s.CommitOrder("O3", []OrderLine{{Product: "P", Qty: 999}}, 6, true, 10, 3)
	mustReject(t, cerr, ReasonPermanentShortage)
	if got := s.Clock(); got != 5 {
		t.Fatalf("时钟 = %d，期望 5（被拒绝的操作不推进时钟）", got)
	}
	mustATP(t, s, "W1", "P", 6, 10-3)
	if _, err := s.ReservationDetails("O3"); err == nil || err.Reason != ReasonOrderNotFound {
		t.Fatalf("被拒绝的订单不应留下预留: %v", err)
	}
	mustOK(t, s.Validate())
}
