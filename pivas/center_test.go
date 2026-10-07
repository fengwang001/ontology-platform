package pivas_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/pivas"
)

func mustCenter(t *testing.T, roomT, coldT int64) *pivas.Center {
	t.Helper()
	c, err := pivas.NewCenter(roomT, coldT)
	if err != nil {
		t.Fatalf("NewCenter: %v", err)
	}
	return c
}

func mustDrug(t *testing.T, c *pivas.Center, now int64, d pivas.Drug) {
	t.Helper()
	if err := c.UpsertDrug(now, d); err != nil {
		t.Fatalf("UpsertDrug %+v: %v", d, err)
	}
}

func mustBench(t *testing.T, c *pivas.Center, now int64, cfg pivas.BenchConfig) {
	t.Helper()
	if err := c.RegisterBench(now, cfg); err != nil {
		t.Fatalf("RegisterBench %+v: %v", cfg, err)
	}
}

func codeOf(t *testing.T, err error) pivas.Code {
	t.Helper()
	var pe *pivas.Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	t.Fatalf("错误类型不是 *pivas.Error: %v", err)
	return 0
}

func admitErr(t *testing.T, c *pivas.Center, now int64, o pivas.Order) pivas.Code {
	t.Helper()
	_, err := c.Admit(now, o)
	if err == nil {
		t.Fatalf("Admit %+v 期望失败却成功", o)
	}
	return codeOf(t, err)
}

func admitOK(t *testing.T, c *pivas.Center, now int64, o pivas.Order) pivas.Admission {
	t.Helper()
	a, err := c.Admit(now, o)
	if err != nil {
		t.Fatalf("Admit %+v: %v", o, err)
	}
	if err := c.VerifyInvariants(); err != nil {
		t.Fatalf("受理后不变量被破坏: %v", err)
	}
	return a
}

// stdBench 登记常用台：容量 2、时长 10/20、清场 10。
func stdBench(t *testing.T, c *pivas.Center, now int64, id string) {
	t.Helper()
	mustBench(t, c, now, pivas.BenchConfig{
		ID: id, Capacity: 2, DurationByCount: []int64{0, 10, 20}, ClearanceSec: 10,
	})
}

func stdOrder(id string, drugs ...string) pivas.Order {
	return pivas.Order{ID: id, DrugIDs: drugs, Solvent: "NS", RequiredAt: 1000}
}

// 有效期恰到期视为已失效（拒绝），提前一秒则可行。
func TestValidityBoundary(t *testing.T) {
	mk := func(roomT, coldT, coldStable int64) *pivas.Center {
		c := mustCenter(t, roomT, coldT)
		mustDrug(t, c, 0, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: coldStable, SolventClass: "NS"})
		mustBench(t, c, 0, pivas.BenchConfig{ID: "B1", Capacity: 1, DurationByCount: []int64{0, 10}, ClearanceSec: 5})
		return c
	}
	// 室温运送 100 == 稳定 100：送达恰到期，已失效 → 无可行安排
	if got := admitErr(t, mk(100, 200, 100), 0, stdOrder("o1", "A")); got != pivas.CodeNoFeasibleSlot {
		t.Fatalf("恰到期应无可行安排, got %v", got)
	}
	// 室温运送 99：送达早一秒，在有效期内
	a := admitOK(t, mk(99, 200, 100), 0, stdOrder("o1", "A"))
	if a.Delivery != 109 || a.Storage != pivas.StorageRoom {
		t.Fatalf("期望室温 109 送达, got %+v", a)
	}
	// 冷藏运送 50 == 冷藏稳定 50：恰到期失效；室温运送 200 亦不可行
	if got := admitErr(t, mk(200, 50, 50), 0, stdOrder("o1", "A")); got != pivas.CodeNoFeasibleSlot {
		t.Fatalf("冷藏恰到期应无可行安排, got %v", got)
	}
	// 冷藏运送 49：仅冷藏可行
	a = admitOK(t, mk(200, 49, 50), 0, stdOrder("o1", "A"))
	if a.Storage != pivas.StorageCold || a.Delivery != 59 {
		t.Fatalf("期望冷藏 59 送达, got %+v", a)
	}
}

// 送达恰等于要求时刻视为按时；晚一秒则拒绝。
func TestDeliveryExactlyAtRequired(t *testing.T) {
	c := mustCenter(t, 5, 7)
	mustDrug(t, c, 0, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	mustBench(t, c, 0, pivas.BenchConfig{ID: "B1", Capacity: 1, DurationByCount: []int64{0, 10}, ClearanceSec: 5})
	o := stdOrder("o1", "A")
	o.RequiredAt = 15 // 0 + 10 + 5 = 15，恰等于
	a := admitOK(t, c, 0, o)
	if a.Delivery != 15 {
		t.Fatalf("期望 15 送达, got %d", a.Delivery)
	}
	o2 := stdOrder("o2", "A")
	o2.RequiredAt = 14 // 晚一秒
	if got := admitErr(t, c, 0, o2); got != pivas.CodeNoFeasibleSlot {
		t.Fatalf("晚一秒应无可行安排, got %v", got)
	}
}

// 冷藏与室温两种存放的取舍：均可行时取送达最早者；一种不可行时取另一种。
func TestStorageTradeoff(t *testing.T) {
	c := mustCenter(t, 10, 40)
	mustDrug(t, c, 0, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: 500, SolventClass: "NS"})
	mustDrug(t, c, 0, pivas.Drug{ID: "B", RoomStableSec: 10, ColdStableSec: 500, SolventClass: "NS"})
	mustBench(t, c, 0, pivas.BenchConfig{ID: "B1", Capacity: 1, DurationByCount: []int64{0, 10}, ClearanceSec: 5})
	// 室温送达 20、冷藏送达 50，均可行 → 室温
	if a := admitOK(t, c, 0, stdOrder("o1", "A")); a.Storage != pivas.StorageRoom || a.Delivery != 20 {
		t.Fatalf("期望室温 20 送达, got %+v", a)
	}
	// B 室温稳定 10 <= 运送 10，只能冷藏
	if a := admitOK(t, c, 0, stdOrder("o2", "B")); a.Storage != pivas.StorageCold || a.Delivery != 65 {
		t.Fatalf("期望冷藏 65 送达, got %+v", a)
	}
	// 冷藏运送更快时选冷藏
	c2 := mustCenter(t, 100, 5)
	mustDrug(t, c2, 0, pivas.Drug{ID: "A", RoomStableSec: 1000, ColdStableSec: 100, SolventClass: "NS"})
	mustBench(t, c2, 0, pivas.BenchConfig{ID: "B1", Capacity: 1, DurationByCount: []int64{0, 10}, ClearanceSec: 5})
	if a := admitOK(t, c2, 0, stdOrder("o1", "A")); a.Storage != pivas.StorageCold || a.Delivery != 15 {
		t.Fatalf("期望冷藏 15 送达, got %+v", a)
	}
}

// 含须避光药品而未用避光外袋 → 避光冲突；批次按外袋形态分组。
func TestLightConflictAndBatchGrouping(t *testing.T) {
	c := mustCenter(t, 5, 8)
	mustDrug(t, c, 0, pivas.Drug{ID: "L", RoomStableSec: 100, ColdStableSec: 100, LightSensitive: true, SolventClass: "NS"})
	mustDrug(t, c, 0, pivas.Drug{ID: "P", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	mustBench(t, c, 0, pivas.BenchConfig{ID: "B1", Capacity: 3, DurationByCount: []int64{0, 10, 20, 30}, ClearanceSec: 10})

	admitOK(t, c, 0, stdOrder("o0", "P")) // 占位批次，开始时刻 0
	bad := pivas.Order{ID: "oL1", DrugIDs: []string{"L"}, Solvent: "NS", RequiredAt: 1000, LightProofBag: false}
	if got := admitErr(t, c, 0, bad); got != pivas.CodeLightConflict {
		t.Fatalf("期望避光冲突, got %v", got)
	}
	good := pivas.Order{ID: "oL2", DrugIDs: []string{"L"}, Solvent: "NS", RequiredAt: 1000, LightProofBag: true}
	a1 := admitOK(t, c, 0, good)
	good2 := pivas.Order{ID: "oL3", DrugIDs: []string{"L"}, Solvent: "NS", RequiredAt: 1000, LightProofBag: true}
	a2 := admitOK(t, c, 0, good2)
	if a1.BatchID != a2.BatchID {
		t.Fatalf("同为避光批次应同批: %s vs %s", a1.BatchID, a2.BatchID)
	}
	a3 := admitOK(t, c, 0, stdOrder("oP2", "P"))
	if a3.BatchID == a1.BatchID {
		t.Fatalf("非避光医嘱不得编入避光批次")
	}
}

// 批容量恰满：第 2 张入批，第 3 张只能新开。
func TestCapacityExactlyFull(t *testing.T) {
	c := mustCenter(t, 5, 8)
	mustDrug(t, c, 0, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	stdBench(t, c, 0, "B1")
	admitOK(t, c, 0, stdOrder("o1", "A")) // B1: [0,10)，运行中
	a2 := admitOK(t, c, 0, stdOrder("o2", "A"))
	a3 := admitOK(t, c, 0, stdOrder("o3", "A"))
	if a2.BatchID != a3.BatchID {
		t.Fatalf("容量恰满前应同批: %s vs %s", a2.BatchID, a3.BatchID)
	}
	a4 := admitOK(t, c, 0, stdOrder("o4", "A"))
	if a4.BatchID == a2.BatchID {
		t.Fatalf("容量已满不得再入批")
	}
	if a4.Start != 50 { // 20 + 20(时长) + 10(清场)
		t.Fatalf("新批次开始时刻期望 50, got %d", a4.Start)
	}
}

// 清场间隔：新批次开始时刻恰好等于上一批结束+间隔；
// 紧急顺延恰好够（ successor 送达恰等于要求）成功，差一秒整体拒绝。
func TestClearanceExactAndUrgentCascade(t *testing.T) {
	mk := func(o3Required int64) *pivas.Center {
		c := mustCenter(t, 5, 8)
		mustDrug(t, c, 0, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
		mustDrug(t, c, 0, pivas.Drug{ID: "C", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "GS"})
		stdBench(t, c, 0, "B1")
		admitOK(t, c, 0, stdOrder("o1", "A")) // B1 批次: [0,10)
		a2 := admitOK(t, c, 0, stdOrder("o2", "A"))
		if a2.Start != 20 { // 恰好 10 + 10
			t.Fatalf("清场间隔恰好够：新批次应 20 开始, got %d", a2.Start)
		}
		o3 := stdOrder("o3", "C") // 溶媒不同，自成 B3
		o3.Solvent = "GS"
		o3.RequiredAt = o3Required
		admitOK(t, c, 0, o3) // B3: [40,50)，送达 55
		return c
	}
	// 恰好够：o3 要求 65，顺延后送达 65 恰等于 → 成功
	c1 := mk(65)
	o4 := stdOrder("o4", "A")
	o4.Urgent = true
	o4.RequiredAt = 50
	a4 := admitOK(t, c1, 0, o4)
	if a4.Delivery != 45 { // 加入 B2：结束 40，送达 45
		t.Fatalf("紧急医嘱送达期望 45, got %d", a4.Delivery)
	}
	if !strings.Contains(c1.DumpState(), "batch B3 bench=B1 start=50") {
		t.Fatalf("B3 应顺延至 50:\n%s", c1.DumpState())
	}
	// 差一秒：o3 要求 64，顺延后送达 65 超时 → 紧急医嘱整体拒绝，状态不变
	c2 := mk(64)
	before := c2.DumpState()
	if got := admitErr(t, c2, 0, o4); got != pivas.CodeNoFeasibleSlot {
		t.Fatalf("连锁顺延破坏约束应整体拒绝, got %v", got)
	}
	if after := c2.DumpState(); after != before {
		t.Fatalf("被拒绝的紧急医嘱不得改变状态")
	}
}

// 普通医嘱加入既有批次不得改变后续批次开始时刻（只能入队尾批）；
// 紧急医嘱可连锁顺延。
func TestNormalJoinCannotShiftSuccessors(t *testing.T) {
	c := mustCenter(t, 5, 8)
	mustDrug(t, c, 0, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	mustDrug(t, c, 0, pivas.Drug{ID: "C", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "GS"})
	stdBench(t, c, 0, "B1")
	admitOK(t, c, 0, stdOrder("o1", "A")) // B1: [0,10)
	a2 := admitOK(t, c, 0, stdOrder("o2", "A"))
	o3 := stdOrder("o3", "C")
	o3.Solvent = "GS"
	a3 := admitOK(t, c, 0, o3) // B3: [40,50)
	// 普通医嘱不能加入 B2（会顺延 B3），只能加入队尾 B3？溶媒不同 → 新开 B4
	a4 := admitOK(t, c, 0, stdOrder("o4", "A"))
	if a4.BatchID == a2.BatchID {
		t.Fatalf("普通医嘱不得以使后续批次顺延的方式加入 B2")
	}
	// 紧急医嘱允许顺延：加入 B2，B3 顺延至 50
	o5 := stdOrder("o5", "A")
	o5.Urgent = true
	a5 := admitOK(t, c, 0, o5)
	if a5.BatchID != a2.BatchID {
		t.Fatalf("紧急医嘱应加入 B2, got %s", a5.BatchID)
	}
	_ = a3
	if !strings.Contains(c.DumpState(), "batch B3 bench=B1 start=50") {
		t.Fatalf("B3 应顺延至 50:\n%s", c.DumpState())
	}
}

// 目录变更只对变更之后受理的医嘱生效。
func TestCatalogChangeNotRetroactive(t *testing.T) {
	c := mustCenter(t, 5, 8)
	mustDrug(t, c, 0, pivas.Drug{ID: "D1", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	mustDrug(t, c, 0, pivas.Drug{ID: "D2", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	mustDrug(t, c, 0, pivas.Drug{ID: "D3", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	stdBench(t, c, 0, "B1")
	admitOK(t, c, 0, stdOrder("o1", "D1", "D2")) // 受理时无禁忌
	if err := c.AddIncompatibility(1, "D1", "D2"); err != nil {
		t.Fatalf("AddIncompatibility: %v", err)
	}
	if got := admitErr(t, c, 1, stdOrder("o2", "D1", "D2")); got != pivas.CodeIncompatiblePair {
		t.Fatalf("变更后应判禁忌配对, got %v", got)
	}
	if !strings.Contains(c.DumpState(), "order o1 state=scheduled") {
		t.Fatalf("已受理医嘱不受目录变更追溯:\n%s", c.DumpState())
	}
	// 稳定秒数变更同样不追溯
	a3 := admitOK(t, c, 2, stdOrder("o3", "D3"))
	if a3.Storage != pivas.StorageRoom {
		t.Fatalf("变更前室温可行, got %v", a3.Storage)
	}
	mustDrug(t, c, 3, pivas.Drug{ID: "D3", RoomStableSec: 5, ColdStableSec: 100, SolventClass: "NS"})
	a4 := admitOK(t, c, 4, stdOrder("o4", "D3"))
	if a4.Storage != pivas.StorageCold {
		t.Fatalf("变更后室温不可行应选冷藏, got %v", a4.Storage)
	}
}

// 取消：时长随数量变化、批次不得提前开始、已开始报状态不符。
func TestCancel(t *testing.T) {
	c := mustCenter(t, 5, 8)
	mustDrug(t, c, 0, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	mustBench(t, c, 0, pivas.BenchConfig{ID: "B1", Capacity: 2, DurationByCount: []int64{0, 10, 20}, ClearanceSec: 10})
	admitOK(t, c, 0, stdOrder("o1", "A")) // B1: [0,10)，已开始
	admitOK(t, c, 0, stdOrder("o2", "A")) // B2: [20,30)
	admitOK(t, c, 0, stdOrder("o3", "A")) // 入 B2: [20,40)
	admitOK(t, c, 0, stdOrder("o4", "A")) // B3: [50,60)

	if err := c.Cancel(0, "o3"); err != nil {
		t.Fatalf("取消未开始医嘱应可行: %v", err)
	}
	dump := c.DumpState()
	if !strings.Contains(dump, "batch B2 bench=B1 start=20 end=30") {
		t.Fatalf("取消后时长应随数量减少、开始不提前:\n%s", dump)
	}
	if !strings.Contains(dump, "batch B3 bench=B1 start=50") {
		t.Fatalf("后续批次不得因取消提前:\n%s", dump)
	}
	// 取消 o2：B2 变空被移除，B3 仍不提前
	if err := c.Cancel(0, "o2"); err != nil {
		t.Fatalf("取消应可行: %v", err)
	}
	dump = c.DumpState()
	if strings.Contains(dump, "batch B2 ") || !strings.Contains(dump, "batch B3 bench=B1 start=50") {
		t.Fatalf("空批次应移除且 B3 不提前:\n%s", dump)
	}
	// 已开始的医嘱取消报状态不符
	if err := c.Cancel(0, "o1"); codeOf(t, err) != pivas.CodeStateConflict {
		t.Fatalf("已开始医嘱取消应状态不符, got %v", err)
	}
	// 重复取消报状态不符
	if err := c.Cancel(0, "o2"); codeOf(t, err) != pivas.CodeStateConflict {
		t.Fatalf("重复取消应状态不符, got %v", err)
	}
	// 未知医嘱
	if err := c.Cancel(0, "nope"); codeOf(t, err) != pivas.CodeOrderNotFound {
		t.Fatalf("未知医嘱应报医嘱不存在, got %v", err)
	}
	if err := c.VerifyInvariants(); err != nil {
		t.Fatalf("取消后不变量被破坏: %v", err)
	}
}

// 时钟回退与被拒绝操作不改变时钟。
func TestClockRollback(t *testing.T) {
	c := mustCenter(t, 5, 8)
	mustDrug(t, c, 10, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	before := c.DumpState()
	if err := c.UpsertDrug(9, pivas.Drug{ID: "B", RoomStableSec: 1, ColdStableSec: 1, SolventClass: "NS"}); codeOf(t, err) != pivas.CodeClockRollback {
		t.Fatalf("应报时钟回退, got %v", err)
	}
	if c.DumpState() != before {
		t.Fatalf("被拒绝的操作不得改变状态")
	}
	// 被拒绝的受理不推进时钟
	stdBench(t, c, 10, "B1")
	o := stdOrder("o1", "A")
	o.RequiredAt = 1 // 必然无可行安排
	if got := admitErr(t, c, 20, o); got != pivas.CodeNoFeasibleSlot {
		t.Fatalf("期望无可行安排, got %v", got)
	}
	// 时钟仍停留在 10：now=10 可用，now=9 仍回退
	mustDrug(t, c, 10, pivas.Drug{ID: "C", RoomStableSec: 1, ColdStableSec: 1, SolventClass: "NS"})
	if err := c.UpsertDrug(9, pivas.Drug{ID: "D", RoomStableSec: 1, ColdStableSec: 1, SolventClass: "NS"}); codeOf(t, err) != pivas.CodeClockRollback {
		t.Fatalf("应报时钟回退, got %v", err)
	}
}

// 错误按优先级只报第一个。
func TestErrorPriority(t *testing.T) {
	c := mustCenter(t, 5, 8)
	mustDrug(t, c, 10, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: 100, LightSensitive: true, SolventClass: "NS"})
	mustDrug(t, c, 10, pivas.Drug{ID: "B", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "GS"})
	if err := c.AddIncompatibility(10, "A", "B"); err != nil {
		t.Fatal(err)
	}
	stdBench(t, c, 10, "B1")

	// 参数非法 优先于 时钟回退
	bad := pivas.Order{ID: "x", DrugIDs: nil, Solvent: "NS", RequiredAt: 100}
	if got := admitErr(t, c, 0, bad); got != pivas.CodeInvalidParam {
		t.Fatalf("参数非法应最先报, got %v", got)
	}
	// 时钟回退 优先于 药品不存在
	o := pivas.Order{ID: "x", DrugIDs: []string{"ZZZ"}, Solvent: "NS", RequiredAt: 100}
	if got := admitErr(t, c, 5, o); got != pivas.CodeClockRollback {
		t.Fatalf("时钟回退应优先于药品不存在, got %v", got)
	}
	// 药品不存在 优先于 禁忌配对
	o = pivas.Order{ID: "x", DrugIDs: []string{"A", "B", "ZZZ"}, Solvent: "NS", RequiredAt: 100}
	if got := admitErr(t, c, 20, o); got != pivas.CodeDrugNotFound {
		t.Fatalf("药品不存在应优先于禁忌配对, got %v", got)
	}
	// 禁忌配对 优先于 溶媒不兼容（B 的溶媒类别与所选不符）
	o = pivas.Order{ID: "x", DrugIDs: []string{"A", "B"}, Solvent: "NS", RequiredAt: 100}
	if got := admitErr(t, c, 20, o); got != pivas.CodeIncompatiblePair {
		t.Fatalf("禁忌配对应优先于溶媒不兼容, got %v", got)
	}
	// 溶媒不兼容 优先于 避光冲突
	o = pivas.Order{ID: "x", DrugIDs: []string{"A", "B"}, Solvent: "W", RequiredAt: 100}
	if err := c.RemoveIncompatibility(20, "A", "B"); err != nil {
		t.Fatal(err)
	}
	if got := admitErr(t, c, 20, o); got != pivas.CodeSolventMismatch {
		t.Fatalf("溶媒不兼容应优先于避光冲突, got %v", got)
	}
	// 避光冲突 优先于 无可行安排（A 须避光而未用避光袋，且要求时刻必然不可行）
	o = pivas.Order{ID: "x", DrugIDs: []string{"A"}, Solvent: "NS", RequiredAt: 0, LightProofBag: false}
	if got := admitErr(t, c, 20, o); got != pivas.CodeLightConflict {
		t.Fatalf("避光冲突应优先于无可行安排, got %v", got)
	}
}

// 参数非法的各种形态。
func TestInvalidParams(t *testing.T) {
	if _, err := pivas.NewCenter(0, 5); codeOf(t, err) != pivas.CodeInvalidParam {
		t.Fatalf("运送时长为零应参数非法, got %v", err)
	}
	c := mustCenter(t, 5, 8)
	mustDrug(t, c, 0, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	stdBench(t, c, 0, "B1")

	cases := []pivas.Order{
		{ID: "", DrugIDs: []string{"A"}, Solvent: "NS", RequiredAt: 100},                                // 空医嘱标识
		{ID: "x", DrugIDs: nil, Solvent: "NS", RequiredAt: 100},                                         // 无药品
		{ID: "x", DrugIDs: []string{"A", "A", "A", "A", "A", "A", "A"}, Solvent: "NS", RequiredAt: 100}, // 超过 6 种
		{ID: "x", DrugIDs: []string{"A", ""}, Solvent: "NS", RequiredAt: 100},                           // 空药品标识
		{ID: "x", DrugIDs: []string{"A", "A"}, Solvent: "NS", RequiredAt: 100},                          // 药品重复
		{ID: "x", DrugIDs: []string{"A"}, Solvent: "", RequiredAt: 100},                                 // 空溶媒
		{ID: "x", DrugIDs: []string{"A"}, Solvent: "NS", RequiredAt: -1},                                // 要求时刻为负
		{ID: "x", DrugIDs: []string{"A"}, Solvent: "NS", RequiredAt: pivas.MaxNow + 1},                  // 要求时刻越界
	}
	for i, o := range cases {
		if got := admitErr(t, c, 0, o); got != pivas.CodeInvalidParam {
			t.Fatalf("用例 %d 应参数非法, got %v", i, got)
		}
	}
	// now 越界
	o := stdOrder("o1", "A")
	if got := admitErr(t, c, -1, o); got != pivas.CodeInvalidParam {
		t.Fatalf("now 为负应参数非法, got %v", got)
	}
	// 重复医嘱标识
	admitOK(t, c, 0, o)
	if got := admitErr(t, c, 0, o); got != pivas.CodeInvalidParam {
		t.Fatalf("重复医嘱标识应参数非法, got %v", got)
	}
	// 药品登记非法
	if err := c.UpsertDrug(0, pivas.Drug{ID: "", RoomStableSec: 1, ColdStableSec: 1, SolventClass: "NS"}); codeOf(t, err) != pivas.CodeInvalidParam {
		t.Fatalf("空药品标识应参数非法, got %v", err)
	}
	if err := c.UpsertDrug(0, pivas.Drug{ID: "D", RoomStableSec: 0, ColdStableSec: 1, SolventClass: "NS"}); codeOf(t, err) != pivas.CodeInvalidParam {
		t.Fatalf("稳定秒数为零应参数非法, got %v", err)
	}
	// 禁忌配对非法
	if err := c.AddIncompatibility(0, "A", "A"); codeOf(t, err) != pivas.CodeInvalidParam {
		t.Fatalf("相同药品配对应参数非法, got %v", err)
	}
	// 洁净台配置非法
	badBenches := []pivas.BenchConfig{
		{ID: "", Capacity: 1, DurationByCount: []int64{0, 10}, ClearanceSec: 5},
		{ID: "X", Capacity: 0, DurationByCount: []int64{0}, ClearanceSec: 5},
		{ID: "X", Capacity: 1, DurationByCount: []int64{0, 10}, ClearanceSec: 0},
		{ID: "X", Capacity: 2, DurationByCount: []int64{0, 10}, ClearanceSec: 5},     // 对照缺项
		{ID: "X", Capacity: 2, DurationByCount: []int64{0, 10, 10}, ClearanceSec: 5}, // 未严格递增
		{ID: "X", Capacity: 2, DurationByCount: []int64{0, 10, 5}, ClearanceSec: 5},  // 递减
		{ID: "X", Capacity: 1, DurationByCount: []int64{0, 0}, ClearanceSec: 5},      // 时长为零
	}
	for i, cfg := range badBenches {
		if err := c.RegisterBench(0, cfg); codeOf(t, err) != pivas.CodeInvalidParam {
			t.Fatalf("台配置用例 %d 应参数非法, got %v", i, err)
		}
	}
	if err := c.RegisterBench(0, pivas.BenchConfig{ID: "B1", Capacity: 1, DurationByCount: []int64{0, 10}, ClearanceSec: 5}); codeOf(t, err) != pivas.CodeInvalidParam {
		t.Fatalf("台编号重复应参数非法, got %v", err)
	}
}

// 禁忌配对为无序配对；溶媒类别须与所选溶媒一致。
func TestIncompatibilityAndSolvent(t *testing.T) {
	c := mustCenter(t, 5, 8)
	mustDrug(t, c, 0, pivas.Drug{ID: "A", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	mustDrug(t, c, 0, pivas.Drug{ID: "B", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "NS"})
	mustDrug(t, c, 0, pivas.Drug{ID: "G", RoomStableSec: 100, ColdStableSec: 100, SolventClass: "GS"})
	stdBench(t, c, 0, "B1")
	if err := c.AddIncompatibility(0, "A", "B"); err != nil {
		t.Fatal(err)
	}
	// 无序：医嘱内顺序无关
	if got := admitErr(t, c, 0, stdOrder("o1", "B", "A")); got != pivas.CodeIncompatiblePair {
		t.Fatalf("禁忌配对与顺序无关, got %v", got)
	}
	// 移除后可行
	if err := c.RemoveIncompatibility(0, "B", "A"); err != nil {
		t.Fatal(err)
	}
	admitOK(t, c, 0, stdOrder("o2", "A", "B"))
	// 溶媒类别不符
	o := stdOrder("o3", "A", "G")
	if got := admitErr(t, c, 0, o); got != pivas.CodeSolventMismatch {
		t.Fatalf("溶媒类别不兼容, got %v", got)
	}
	o.Solvent = "GS"
	if got := admitErr(t, c, 0, o); got != pivas.CodeSolventMismatch {
		t.Fatalf("A 的类别为 NS，选 GS 亦不兼容, got %v", got)
	}
}

// 并发调用：结果等价于某个串行顺序，已受理医嘱始终满足约束。
func TestConcurrentOps(t *testing.T) {
	c := mustCenter(t, 5, 8)
	mustDrug(t, c, 0, pivas.Drug{ID: "A", RoomStableSec: 10000, ColdStableSec: 10000, SolventClass: "NS"})
	mustBench(t, c, 0, pivas.BenchConfig{ID: "B1", Capacity: 3, DurationByCount: []int64{0, 10, 20, 30}, ClearanceSec: 10})
	mustBench(t, c, 0, pivas.BenchConfig{ID: "B2", Capacity: 2, DurationByCount: []int64{0, 8, 16}, ClearanceSec: 5})

	var wg sync.WaitGroup
	var nowGen atomic.Int64
	var accepted atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := nowGen.Add(1)
				o := pivas.Order{
					ID:         fmt.Sprintf("g%d-o%d", g, i),
					DrugIDs:    []string{"A"},
					Solvent:    "NS",
					RequiredAt: now + 5000,
					Urgent:     i%3 == 0,
				}
				a, err := c.Admit(now, o)
				if err != nil {
					continue // 时钟回退或无可行安排均允许
				}
				if a.Delivery > o.RequiredAt {
					t.Errorf("送达晚于要求时刻: %+v", a)
				}
				accepted.Add(1)
				if i%5 == 0 {
					_ = c.Cancel(nowGen.Add(1), o.ID)
				}
			}
		}(g)
	}
	wg.Wait()
	if accepted.Load() == 0 {
		t.Fatalf("并发下应有受理成功")
	}
	if err := c.VerifyInvariants(); err != nil {
		t.Fatalf("并发后不变量被破坏: %v", err)
	}
}
