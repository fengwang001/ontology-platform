package recall

import (
	"sync/atomic"
	"testing"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s 应成功, 实际=%v", ctx, err)
	}
}

func errCode(err error) ErrorCode {
	if err == nil {
		return 0
	}
	e, _ := AsOpError(err)
	return e.Code
}

// 批号区间两端恰取等（字典序闭区间）。
func TestLotRangeEndpointsInclusive(t *testing.T) {
	s := New()
	mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "A", Quantity: 10}), "入库A")
	mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "B", Quantity: 10}), "入库B")
	mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "C", Quantity: 10}), "入库C")
	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 2, RecallID: "R1", DrugID: "D",
		LotLow: "B", LotHigh: "B", Level: 1, IssueAt: 0}), "登记[B,B]")

	for _, c := range []struct {
		batch string
		lv    int
	}{{"A", 0}, {"B", 1}, {"C", 0}} {
		info, err := s.QueryBatch(BatchQueryReq{Now: 2, DrugID: "D", BatchID: c.batch})
		mustOK(t, err, "查询"+c.batch)
		if info.EffectiveLevel != c.lv {
			t.Fatalf("批号 %s 等级应为 %d, 实际 %d", c.batch, c.lv, info.EffectiveLevel)
		}
	}
	// 字典序：B10 < B2，数值直觉会错，这里钉住字符串比较语义。
	mustOK(t, s.Inbound(InboundReq{Now: 3, DrugID: "D", BatchID: "B10", Quantity: 5}), "入库B10")
	info, _ := s.QueryBatch(BatchQueryReq{Now: 3, DrugID: "D", BatchID: "B10"})
	if info.EffectiveLevel != 0 {
		t.Fatalf("字典序下 B10 不应被 [B,B] 覆盖, 等级=%d", info.EffectiveLevel)
	}
}

// 召回登记后才入库的批次同样受约束。
func TestInboundAfterRecallIsBound(t *testing.T) {
	s := New()
	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 1, RecallID: "R1", DrugID: "D",
		LotLow: "L1", LotHigh: "L9", Level: 1, IssueAt: 0}), "先登记")
	mustOK(t, s.Inbound(InboundReq{Now: 2, DrugID: "D", BatchID: "L5", Quantity: 7}), "后入库")
	info, _ := s.QueryBatch(BatchQueryReq{Now: 2, DrugID: "D", BatchID: "L5"})
	if info.EffectiveLevel != 1 || len(info.ActiveRecallIDs) != 1 ||
		info.ActiveRecallIDs[0] != "R1" {
		t.Fatalf("后入库批次应被召回覆盖: %+v", info)
	}
	err := s.Transfer(TransferReq{Now: 3, DrugID: "D", BatchID: "L5",
		From: Warehouse, To: "病区药柜-A", Quantity: 1})
	if errCode(err) != CodeRecallForbidden {
		t.Fatalf("一级冻结下调拨应被禁止, 实际=%v", err)
	}
}

// 多条召回叠加取最严；并列全部列出；解除一条后等级回落。
func TestRecallStackingAndRelease(t *testing.T) {
	s := New()
	mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "L5", Quantity: 20}), "入库")

	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 2, RecallID: "R3a", DrugID: "D",
		LotLow: "L1", LotHigh: "L9", Level: 3, IssueAt: 0}), "三级a")
	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 2, RecallID: "R3b", DrugID: "D",
		LotLow: "L1", LotHigh: "L9", Level: 3, IssueAt: 0}), "三级b并列")
	info, _ := s.QueryBatch(BatchQueryReq{Now: 2, DrugID: "D", BatchID: "L5"})
	if info.EffectiveLevel != 3 || len(info.ActiveRecallIDs) != 2 {
		t.Fatalf("并列三级应全部列出: %+v", info)
	}

	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 3, RecallID: "R1", DrugID: "D",
		LotLow: "L1", LotHigh: "L9", Level: 1, IssueAt: 0}), "一级叠加")
	info, _ = s.QueryBatch(BatchQueryReq{Now: 3, DrugID: "D", BatchID: "L5"})
	if info.EffectiveLevel != 1 || len(info.ActiveRecallIDs) != 1 ||
		info.ActiveRecallIDs[0] != "R1" {
		t.Fatalf("叠加后应只列最严一级: %+v", info)
	}

	mustOK(t, s.ReleaseRecall(ReleaseRecallReq{Now: 4, RecallID: "R1"}), "解除一级")
	info, _ = s.QueryBatch(BatchQueryReq{Now: 4, DrugID: "D", BatchID: "L5"})
	if info.EffectiveLevel != 3 || len(info.ActiveRecallIDs) != 2 {
		t.Fatalf("解除后应回落并列三级: %+v", info)
	}

	mustOK(t, s.ReleaseRecall(ReleaseRecallReq{Now: 5, RecallID: "R3a"}), "解除a")
	mustOK(t, s.ReleaseRecall(ReleaseRecallReq{Now: 5, RecallID: "R3b"}), "解除b")
	info, _ = s.QueryBatch(BatchQueryReq{Now: 5, DrugID: "D", BatchID: "L5"})
	if info.EffectiveLevel != 0 || len(info.ActiveRecallIDs) != 0 {
		t.Fatalf("全部解除后应无召回: %+v", info)
	}
}

// 一级/二级/三级 × 发放/调拨 矩阵。
func TestLevelMatrix(t *testing.T) {
	type tc struct {
		level    int
		consent  bool
		dispense ErrorCode
		toWard   ErrorCode
		toStore  ErrorCode
	}
	cases := []tc{
		{1, true, CodeRecallForbidden, CodeRecallForbidden, CodeRecallForbidden},
		{2, true, CodeRecallForbidden, CodeRecallForbidden, 0},
		{3, false, CodeConsentRequired, 0, 0},
		{3, true, 0, 0, 0},
	}
	for idx, c := range cases {
		s := New()
		mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "L5", Quantity: 50}), "入库")
		mustOK(t, s.Transfer(TransferReq{Now: 1, DrugID: "D", BatchID: "L5",
			From: Warehouse, To: "病区药柜-A", Quantity: 20}), "预调拨")
		mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 2, RecallID: "R", DrugID: "D",
			LotLow: "L1", LotHigh: "L9", Level: c.level, IssueAt: 0}), "登记")

		if err := s.Dispense(DispenseReq{Now: 3, DrugID: "D", BatchID: "L5",
			Location: "病区药柜-A", Patient: "P", Quantity: 1, Consent: c.consent}); errCode(err) != c.dispense {
			t.Fatalf("用例%d 发放: 期望 %s 实际 %v", idx, c.dispense.Name(), err)
		}
		if err := s.Transfer(TransferReq{Now: 3, DrugID: "D", BatchID: "L5",
			From: "病区药柜-A", To: "病区药柜-B", Quantity: 1}); errCode(err) != c.toWard {
			t.Fatalf("用例%d 调入病区: 期望 %s 实际 %v", idx, c.toWard.Name(), err)
		}
		if err := s.Transfer(TransferReq{Now: 3, DrugID: "D", BatchID: "L5",
			From: "病区药柜-A", To: Warehouse, Quantity: 1}); errCode(err) != c.toStore {
			t.Fatalf("用例%d 调入药库: 期望 %s 实际 %v", idx, c.toStore.Name(), err)
		}
	}

	s := New()
	mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "L5", Quantity: 10}), "入库")
	mustOK(t, s.Dispense(DispenseReq{Now: 2, DrugID: "D", BatchID: "L5",
		Location: Warehouse, Patient: "P", Quantity: 1}), "无召回发放")
}

// 退药 FIFO 抵扣早期发放、零数量不列、始发时刻恰等于发放时刻。
func TestRecoveryFifoDeductionAndBoundaries(t *testing.T) {
	s := New()
	mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "L5", Quantity: 100}), "入库")
	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 2, RecallID: "R1", DrugID: "D",
		LotLow: "L1", LotHigh: "L9", Level: 1, IssueAt: 0}), "初登记")
	mustOK(t, s.ReleaseRecall(ReleaseRecallReq{Now: 3, RecallID: "R1"}), "临时解除")
	mustOK(t, s.Dispense(DispenseReq{Now: 50, DrugID: "D", BatchID: "L5",
		Location: Warehouse, Patient: "P", Quantity: 10}), "t=50 发10")
	mustOK(t, s.Dispense(DispenseReq{Now: 100, DrugID: "D", BatchID: "L5",
		Location: Warehouse, Patient: "P", Quantity: 10}), "t=100 发10")
	mustOK(t, s.Dispense(DispenseReq{Now: 120, DrugID: "D", BatchID: "L5",
		Location: Warehouse, Patient: "P", Quantity: 10}), "t=120 发10")
	mustOK(t, s.Dispense(DispenseReq{Now: 120, DrugID: "D", BatchID: "L5",
		Location: Warehouse, Patient: "Q", Quantity: 7}), "t=120 发Q7")
	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 130, RecallID: "R2", DrugID: "D",
		LotLow: "L1", LotHigh: "L9", Level: 1, IssueAt: 100}), "重新登记")

	list, err := s.RecoveryList(RecoveryReq{Now: 130, RecallID: "R2"})
	mustOK(t, err, "清单")
	if len(list.Items) != 2 {
		t.Fatalf("初始应有 P/Q 两条: %+v", list.Items)
	}
	want := map[string]int{"P": 20, "Q": 7}
	for _, it := range list.Items {
		if want[it.Patient] != it.Qty {
			t.Fatalf("初始清单数量不符: %+v", it)
		}
	}

	mustOK(t, s.Return(ReturnReq{Now: 140, DrugID: "D", BatchID: "L5",
		Patient: "P", Quantity: 12}), "退12")
	list, _ = s.RecoveryList(RecoveryReq{Now: 140, RecallID: "R2"})
	if len(list.Items) != 2 || list.Items[0].Patient != "P" || list.Items[0].Qty != 18 {
		t.Fatalf("早期抵扣后 P 应为18: %+v", list.Items)
	}

	mustOK(t, s.Return(ReturnReq{Now: 150, DrugID: "D", BatchID: "L5",
		Patient: "Q", Quantity: 7}), "Q全退")
	list, _ = s.RecoveryList(RecoveryReq{Now: 150, RecallID: "R2"})
	if len(list.Items) != 1 || list.Items[0].Patient != "P" {
		t.Fatalf("Q 清零后不应列出: %+v", list.Items)
	}

	info, _ := s.QueryBatch(BatchQueryReq{Now: 150, DrugID: "D", BatchID: "L5"})
	stockSum := 0
	for _, st := range info.Stock {
		stockSum += st.Quantity
	}
	if stockSum != 82 {
		t.Fatalf("库存合计应为82, 实际 %d (%+v)", stockSum, info.Stock)
	}
}

// 三级召回清单状态不符；解除后清单不可查。
func TestRecoveryStateRules(t *testing.T) {
	s := New()
	mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "L5", Quantity: 10}), "入库")
	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 2, RecallID: "R3", DrugID: "D",
		LotLow: "L1", LotHigh: "L9", Level: 3, IssueAt: 0}), "三级")
	if _, err := s.RecoveryList(RecoveryReq{Now: 2, RecallID: "R3"}); errCode(err) != CodeInvalidState {
		t.Fatalf("三级召回清单应状态不符, 实际=%v", err)
	}
	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 3, RecallID: "R1", DrugID: "D",
		LotLow: "L1", LotHigh: "L9", Level: 1, IssueAt: 0}), "一级")
	res, err := s.RecoveryList(RecoveryReq{Now: 3, RecallID: "R1"})
	mustOK(t, err, "一级清单可查")
	if len(res.Items) != 0 {
		t.Fatalf("尚无始发后发放, 清单应为空, 实际 %+v", res.Items)
	}
	mustOK(t, s.ReleaseRecall(ReleaseRecallReq{Now: 4, RecallID: "R1"}), "解除一级")
	if _, err := s.RecoveryList(RecoveryReq{Now: 4, RecallID: "R1"}); errCode(err) != CodeInvalidState {
		t.Fatalf("解除后清单应状态不符, 实际=%v", err)
	}
	if _, err := s.RecoveryList(RecoveryReq{Now: 4, RecallID: "NOPE"}); errCode(err) != CodeNotFound {
		t.Fatalf("不存在召回应对象不存在, 实际=%v", err)
	}
}

// 时钟单调：被接受的操作推进时钟；被拒绝（含回退）的操作不推进时钟。
func TestMonotonicClock(t *testing.T) {
	s := New()
	mustOK(t, s.Inbound(InboundReq{Now: 10, DrugID: "D", BatchID: "B1", Quantity: 5}), "t=10")
	err := s.Inbound(InboundReq{Now: 9, DrugID: "D", BatchID: "B2", Quantity: 5})
	if errCode(err) != CodeClockRollback {
		t.Fatalf("时钟回退应报错, 实际=%v", err)
	}
	// 回退被拒后，now=10 仍应被接受（时钟未被污染），但 B2 并未入库。
	mustOK(t, s.Inbound(InboundReq{Now: 10, DrugID: "D", BatchID: "B3", Quantity: 5}), "t=10仍接受")
	if _, err := s.QueryBatch(BatchQueryReq{Now: 10, DrugID: "D", BatchID: "B2"}); errCode(err) != CodeNotFound {
		t.Fatalf("回退操作不得产生 B2, 实际=%v", err)
	}
	if s.LastNow() != 10 {
		t.Fatalf("时钟应为10, 实际 %d", s.LastNow())
	}
}

// 参数非法与错误优先级：参数非法先于时钟回退；时钟回退先于对象不存在；
// 对象不存在先于库存不足；召回禁止先于知情确认/库存不足；
// 三级下知情确认先于库存不足；退药超量先于状态不符。
func TestErrorPriority(t *testing.T) {
	s := New()
	mustOK(t, s.Inbound(InboundReq{Now: 10, DrugID: "D", BatchID: "B1", Quantity: 5}), "入库")
	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 11, RecallID: "R3", DrugID: "D",
		LotLow: "B1", LotHigh: "B1", Level: 3, IssueAt: 0}), "三级")

	// 空药品 + 时钟回退 => 参数非法优先。
	err := s.Dispense(DispenseReq{Now: 1, DrugID: "", BatchID: "B1",
		Location: Warehouse, Patient: "P", Quantity: 1})
	if errCode(err) != CodeInvalidParam {
		t.Fatalf("参数非法应最先报, 实际=%v", err)
	}
	// 合法参数 + 时钟回退 + 批号不存在 => 时钟回退优先。
	err = s.Transfer(TransferReq{Now: 1, DrugID: "D", BatchID: "NOPE",
		From: Warehouse, To: "病区药柜-A", Quantity: 100})
	if errCode(err) != CodeClockRollback {
		t.Fatalf("时钟回退应先于对象不存在, 实际=%v", err)
	}
	// 时钟正常 + 批号不存在 + 数量超发 => 对象不存在优先。
	err = s.Transfer(TransferReq{Now: 12, DrugID: "D", BatchID: "NOPE",
		From: Warehouse, To: "病区药柜-A", Quantity: 100})
	if errCode(err) != CodeNotFound {
		t.Fatalf("对象不存在应先于库存不足, 实际=%v", err)
	}
	// 三级召回 + 不确认 + 库存不足 => 需知情确认优先。
	err = s.Dispense(DispenseReq{Now: 12, DrugID: "D", BatchID: "B1",
		Location: Warehouse, Patient: "P", Quantity: 100, Consent: false})
	if errCode(err) != CodeConsentRequired {
		t.Fatalf("知情确认应先于库存不足, 实际=%v", err)
	}
	// 三级 + 确认 + 库存不足 => 库存不足。
	err = s.Dispense(DispenseReq{Now: 12, DrugID: "D", BatchID: "B1",
		Location: Warehouse, Patient: "P", Quantity: 100, Consent: true})
	if errCode(err) != CodeInsufficientStock {
		t.Fatalf("确认后应报库存不足, 实际=%v", err)
	}
}

// 一级下库存不足仍先报召回禁止（召回禁止先于库存不足）。
func TestForbiddenBeforeStock(t *testing.T) {
	s := New()
	mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "B1", Quantity: 5}), "入库")
	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 2, RecallID: "R1", DrugID: "D",
		LotLow: "B1", LotHigh: "B1", Level: 1, IssueAt: 0}), "一级")
	err := s.Transfer(TransferReq{Now: 3, DrugID: "D", BatchID: "B1",
		From: Warehouse, To: "病区药柜-A", Quantity: 100})
	if errCode(err) != CodeRecallForbidden {
		t.Fatalf("一级应先报召回禁止, 实际=%v", err)
	}
}

// 被拒绝的操作无任何副作用。
func TestRejectionHasNoSideEffects(t *testing.T) {
	s := New()
	mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "B1", Quantity: 10}), "入库")
	mustOK(t, s.Transfer(TransferReq{Now: 2, DrugID: "D", BatchID: "B1",
		From: Warehouse, To: "病区药柜-A", Quantity: 4}), "调拨4")

	before, _ := s.QueryBatch(BatchQueryReq{Now: 2, DrugID: "D", BatchID: "B1"})

	// 超量调拨被拒。
	if err := s.Transfer(TransferReq{Now: 3, DrugID: "D", BatchID: "B1",
		From: "病区药柜-A", To: "病区药柜-B", Quantity: 5}); errCode(err) != CodeInsufficientStock {
		t.Fatalf("应库存不足, 实际=%v", err)
	}
	// 退药超量被拒。
	if err := s.Return(ReturnReq{Now: 3, DrugID: "D", BatchID: "B1",
		Patient: "P", Quantity: 1}); errCode(err) != CodeReturnExceeded {
		t.Fatalf("应退药超量, 实际=%v", err)
	}
	// 重复入库被拒。
	if err := s.Inbound(InboundReq{Now: 3, DrugID: "D", BatchID: "B1", Quantity: 1}); errCode(err) != CodeInvalidState {
		t.Fatalf("重复入库应状态不符, 实际=%v", err)
	}
	// 重复召回编号被拒。
	mustOK(t, s.RegisterRecall(RegisterRecallReq{Now: 4, RecallID: "RX", DrugID: "D",
		LotLow: "B1", LotHigh: "B1", Level: 1, IssueAt: 4}), "登记一级")
	if err := s.RegisterRecall(RegisterRecallReq{Now: 5, RecallID: "RX", DrugID: "D",
		LotLow: "B1", LotHigh: "B1", Level: 1, IssueAt: 5}); errCode(err) != CodeInvalidState {
		t.Fatalf("重复召回编号应状态不符, 实际=%v", err)
	}

	after, _ := s.QueryBatch(BatchQueryReq{Now: 5, DrugID: "D", BatchID: "B1"})
	if len(before.Stock) != len(after.Stock) {
		t.Fatalf("拒绝操作前后库存结构变化: %v -> %v", before.Stock, after.Stock)
	}
	for i := range before.Stock {
		if before.Stock[i] != after.Stock[i] {
			t.Fatalf("拒绝操作改变了库存: %v -> %v", before.Stock, after.Stock)
		}
	}
}

// 退药只进入药库；患者持有量与各位置库存之和恒等于入库量。
func TestReturnGoesToWarehouseAndConservation(t *testing.T) {
	s := New()
	mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "B1", Quantity: 10}), "入库")
	mustOK(t, s.Transfer(TransferReq{Now: 2, DrugID: "D", BatchID: "B1",
		From: Warehouse, To: "病区药柜-A", Quantity: 6}), "调拨6")
	mustOK(t, s.Dispense(DispenseReq{Now: 3, DrugID: "D", BatchID: "B1",
		Location: "病区药柜-A", Patient: "P", Quantity: 5, Consent: true}), "发放5")
	mustOK(t, s.Return(ReturnReq{Now: 4, DrugID: "D", BatchID: "B1",
		Patient: "P", Quantity: 2}), "退2")

	info, _ := s.QueryBatch(BatchQueryReq{Now: 4, DrugID: "D", BatchID: "B1"})
	qty := map[string]int{}
	for _, st := range info.Stock {
		qty[st.Location] = st.Quantity
	}
	if qty[Warehouse] != 6 { // 4 库余 + 2 退回
		t.Fatalf("药库应有6(含退回2), 实际 %d (%+v)", qty[Warehouse], info.Stock)
	}
	if qty["病区药柜-A"] != 1 {
		t.Fatalf("药柜应有1, 实际 %d", qty["病区药柜-A"])
	}
	l := s.inv.ledger("D", "B1")
	if l.heldQty("P") != 3 {
		t.Fatalf("患者应持有3, 实际 %d", l.heldQty("P"))
	}
}

// 参数边界：数量 [1,10^6]，等级 [1,3]，始发时刻不晚于 now。
func TestParameterBounds(t *testing.T) {
	s := New()
	if err := s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "B", Quantity: 0}); errCode(err) != CodeInvalidParam {
		t.Fatalf("数量0应非法, 实际=%v", err)
	}
	if err := s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "B", Quantity: 1_000_001}); errCode(err) != CodeInvalidParam {
		t.Fatalf("数量1000001应非法, 实际=%v", err)
	}
	mustOK(t, s.Inbound(InboundReq{Now: 1, DrugID: "D", BatchID: "B", Quantity: 1_000_000}), "上限数量")
	if err := s.RegisterRecall(RegisterRecallReq{Now: 2, RecallID: "R", DrugID: "D",
		LotLow: "B", LotHigh: "B", Level: 0, IssueAt: 2}); errCode(err) != CodeInvalidParam {
		t.Fatalf("等级0应非法, 实际=%v", err)
	}
	if err := s.RegisterRecall(RegisterRecallReq{Now: 2, RecallID: "R", DrugID: "D",
		LotLow: "B", LotHigh: "B", Level: 3, IssueAt: 3}); errCode(err) != CodeInvalidParam {
		t.Fatalf("始发晚于now应非法, 实际=%v", err)
	}
}

// 并发调用：结果等价于某个串行顺序，守恒始终成立（配合 -race 检测数据竞争）。
// 并发请求各自带原子递增的时间戳；时钟回退或库存瞬时不足都是合法拒绝，
// 取新时间戳重试即可，直到这一对往返调拨成功。无论如何交错，
// 每个被接受的串行状态都满足守恒；终态全部货物应回到药库。
func TestConcurrentSerializable(t *testing.T) {
	s := New()
	mustOK(t, s.Inbound(InboundReq{Now: 0, DrugID: "D", BatchID: "B1", Quantity: 1_000_000}), "入库")
	const goroutines, loops = 50, 40
	var clock int64
	done := make(chan error, goroutines)
	var retries int64
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			ward := "病区药柜-" + string(rune('A'+g%4))
			for i := 0; i < loops; i++ {
				if err := roundTrip(s, &clock, ward, &retries); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}()
	}
	for g := 0; g < goroutines; g++ {
		if err := <-done; err != nil {
			t.Fatalf("并发操作失败: %v", err)
		}
	}
	info, err := s.QueryBatch(BatchQueryReq{Now: 1 << 40, DrugID: "D", BatchID: "B1"})
	mustOK(t, err, "终态查询")
	sum := 0
	for _, st := range info.Stock {
		sum += st.Quantity
	}
	if sum != 1_000_000 || info.EffectiveLevel != 0 {
		t.Fatalf("并发终态守恒破坏: sum=%d level=%d stock=%+v", sum, info.EffectiveLevel, info.Stock)
	}
	t.Logf("并发往返调拨全部成功；合法拒绝后重试次数=%d", retries)
}

// roundTrip 完成一对"药库->药柜->药库"调拨，任一合法拒绝则用更新的时间戳整体重试。
func roundTrip(s *System, clock *int64, ward string, retries *int64) error {
	for {
		t1 := atomic.AddInt64(clock, 1)
		errOut := s.Transfer(TransferReq{Now: t1, DrugID: "D", BatchID: "B1",
			From: Warehouse, To: ward, Quantity: 1})
		switch errCode(errOut) {
		case CodeClockRollback, CodeInsufficientStock:
			atomic.AddInt64(retries, 1)
			continue
		}
		if errOut != nil {
			return errOut
		}
		t2 := atomic.AddInt64(clock, 1)
		errBack := s.Transfer(TransferReq{Now: t2, DrugID: "D", BatchID: "B1",
			From: ward, To: Warehouse, Quantity: 1})
		switch errCode(errBack) {
		case CodeClockRollback, CodeInsufficientStock:
			atomic.AddInt64(retries, 1)
			// 成对重试：先把刚发出的 1 件收回药库（只可能因时钟/库存被拒）。
			for {
				tc := atomic.AddInt64(clock, 1)
				e := s.Transfer(TransferReq{Now: tc, DrugID: "D", BatchID: "B1",
					From: ward, To: Warehouse, Quantity: 1})
				if e == nil {
					break
				}
				if errCode(e) != CodeClockRollback && errCode(e) != CodeInsufficientStock {
					return e
				}
			}
			continue
		}
		if errBack != nil {
			return errBack
		}
		return nil
	}
}
