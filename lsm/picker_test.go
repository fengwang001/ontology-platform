package lsm

import (
	"testing"
)

// TestL0EndpointEqualityExpansion 零层扩展把端点相等也算作重叠。
func TestL0EndpointEqualityExpansion(t *testing.T) {
	s := newTestService(t, testCfg())
	// f1=[10,20] 与 f2=[20,30] 端点相接；f3=[40,50] 不相交。
	mustAdd(t, s, mf(1, 0, 10, 20, 10))
	mustAdd(t, s, mf(2, 0, 20, 30, 10))
	mustAdd(t, s, mf(3, 0, 40, 50, 10))
	mustAdd(t, s, mf(4, 0, 60, 70, 10)) // 凑够触发阈值
	p := mustPick(t, s)
	if p.Level != 0 {
		t.Fatalf("expected L0 plan, got L%d", p.Level)
	}
	// 起点是编号最小的 f1，扩展应纳入端点相等的 f2，但不包括 f3、f4。
	if err := eqIDs(p.Inputs, 1, 2); err != nil {
		t.Fatal(err)
	}
}

// TestL0SeedIsSmallestID 零层起点是编号最小的文件，而不是键最小的文件。
func TestL0SeedIsSmallestID(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 0, 90, 95, 10)) // 最旧但键最大
	mustAdd(t, s, mf(2, 0, 10, 15, 10))
	mustAdd(t, s, mf(3, 0, 20, 25, 10))
	mustAdd(t, s, mf(4, 0, 30, 35, 10))
	p := mustPick(t, s)
	if err := eqIDs(p.Inputs, 1); err != nil {
		t.Fatalf("seed must be smallest-ID file: %v", err)
	}
}

// TestNonL0BidirectionalChainClosure 非零层相接端点的双向闭合与连锁闭合。
func TestNonL0BidirectionalChainClosure(t *testing.T) {
	cfg := testCfg()
	s := newTestService(t, cfg)
	// 先用一个不相干的文件把 lastKey 推到 20。
	mustAdd(t, s, mf(1, 1, 10, 20, 1000))
	p1 := mustPick(t, s) // move f1 -> L2
	if p1.Kind != PlanMove {
		t.Fatalf("expected move plan, got %v", p1.Kind)
	}
	if err := s.Install(p1.ID, []FileMeta{mf(1, 2, 10, 20, 1000)}); err != nil {
		t.Fatalf("install failed: %v", err)
	}
	// 现在 L1 为空，lastKey=20。加入端点相接链 f2,f3,f4。
	mustAdd(t, s, mf(2, 1, 20, 30, 400))
	mustAdd(t, s, mf(3, 1, 30, 40, 400))
	mustAdd(t, s, mf(4, 1, 40, 50, 400))
	p2 := mustPick(t, s)
	// 起点：min 严格大于 20 的最小者 -> f3([30,40])；f2 的 min==20 被严格大于排除。
	// 闭合：f3.max=40==f4.min 纳入 f4（前向）；f3.min=30==f2.max 纳入 f2（反向）。
	if err := eqIDs(p2.Inputs, 2, 3, 4); err != nil {
		t.Fatalf("bidirectional chain closure failed: %v", err)
	}
}

// TestClosureDoesNotSplitUserKey 同一用户键的版本不得被劈开：
// 单键文件链 [k,k]、[k,k] 必须整体纳入。
func TestClosureDoesNotSplitUserKey(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 10, 400))
	mustAdd(t, s, mf(2, 1, 10, 10, 400))
	mustAdd(t, s, mf(3, 1, 10, 10, 400))
	p := mustPick(t, s)
	if err := eqIDs(p.Inputs, 1, 2, 3); err != nil {
		t.Fatalf("point-file chain at same key must be closed as a whole: %v", err)
	}
}

// TestWraparound 上次压实终点键之后没有文件时回绕到最小者。
func TestWraparound(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 20, 1000))
	mustAdd(t, s, mf(2, 1, 30, 40, 1000))
	mustAdd(t, s, mf(3, 1, 50, 60, 1000))
	// 三次直接下移，把 lastKey 一路推到 60。
	for i := uint64(1); i <= 3; i++ {
		p := mustPick(t, s)
		if err := eqIDs(p.Inputs, i); err != nil {
			t.Fatalf("round %d: %v", i, err)
		}
		if p.Kind != PlanMove {
			t.Fatalf("round %d: expected move", i)
		}
		f := mf(i, 2, p.Inputs[0].Smallest[0], p.Inputs[0].Largest[0], 1000)
		if err := s.Install(p.ID, []FileMeta{f}); err != nil {
			t.Fatalf("install failed: %v", err)
		}
	}
	// L1 为空，重新放入两个文件；lastKey=60 之后没有任何 min，必须回绕。
	mustAdd(t, s, mf(4, 1, 10, 20, 1000))
	mustAdd(t, s, mf(5, 1, 30, 40, 1000))
	p := mustPick(t, s)
	if err := eqIDs(p.Inputs, 4); err != nil {
		t.Fatalf("wraparound must restart at smallest min-key: %v", err)
	}
}

// TestOccupancyReselect 在途占用冲突时放弃该层，改选分数次高且不小于一的层。
func TestOccupancyReselect(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 20, 1500))
	mustAdd(t, s, mf(2, 1, 30, 40, 1500)) // L1 分数 3
	mustAdd(t, s, mf(3, 2, 50, 60, 15000))
	mustAdd(t, s, mf(4, 2, 70, 80, 15000)) // L2 分数 3
	p1 := mustPick(t, s)                   // 并列取低层：L1，种子 f1，直接下移
	if p1.Level != 1 {
		t.Fatalf("expected L1 plan, got L%d", p1.Level)
	}
	if !s.Pinned(1) {
		t.Fatalf("input must be pinned after pick")
	}
	// 第二次选取：L1 种子 f1 被占用 -> 放弃 L1，改选 L2。
	p2 := mustPick(t, s)
	if p2.Level != 2 {
		t.Fatalf("expected reselect to L2, got L%d", p2.Level)
	}
	if err := eqIDs(p2.Inputs, 3); err != nil {
		t.Fatal(err)
	}
}

// TestOccupancyReselectStillConflicts 改选后仍冲突则计划为空，且不留任何占用。
func TestOccupancyReselectStillConflicts(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 20, 2000))
	mustAdd(t, s, mf(2, 1, 30, 40, 10)) // 不参与任何计划，用于验证不被误占用
	mustAdd(t, s, mf(3, 2, 10, 20, 20000))
	p1 := mustPick(t, s) // L1 重写：inputs={1}, next={3}
	if p1.Level != 1 || len(p1.NextInputs) != 1 {
		t.Fatalf("unexpected first plan: %v", p1)
	}
	// L1 种子 f1 被占用 -> 放弃；改选 L2，其种子 f3 也被占用 -> 计划为空。
	p2, err := s.Pick()
	if err != nil {
		t.Fatalf("Pick failed: %v", err)
	}
	if p2 != nil {
		t.Fatalf("expected empty plan when all candidates conflict, got %v", p2)
	}
	if !s.Pinned(1) || !s.Pinned(3) {
		t.Fatalf("first plan pins must remain")
	}
	if s.Pinned(2) {
		t.Fatalf("abandoned selection must not leave pins behind")
	}
}

// TestDirectMove 本层只有一个输入且下一层无重叠文件时为直接下移。
func TestDirectMove(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 20, 1000))
	p := mustPick(t, s)
	if p.Kind != PlanMove {
		t.Fatalf("expected move, got %v", p.Kind)
	}
	if err := eqIDs(p.Inputs, 1); err != nil {
		t.Fatal(err)
	}
	if len(p.NextInputs) != 0 {
		t.Fatalf("move plan must have no next-level inputs")
	}
}

// TestEndpointTouchingNextLevelIsNotMove 下一层仅端点相等也算重叠，
// 必须纳入该文件并按重写处理，而不是直接下移。
func TestEndpointTouchingNextLevelIsNotMove(t *testing.T) {
	s := newTestService(t, testCfg())
	mustAdd(t, s, mf(1, 1, 10, 20, 1000))
	mustAdd(t, s, mf(2, 2, 20, 30, 10)) // 与 f1 端点相接
	p := mustPick(t, s)
	if p.Kind != PlanRewrite {
		t.Fatalf("endpoint-touching next-level file forces rewrite, got %v", p.Kind)
	}
	if err := eqIDs(p.NextInputs, 2); err != nil {
		t.Fatal(err)
	}
}

// TestNextLevelClosureDoesNotBackflow 下一层闭合带来的区间扩大
// 不得反过来改变本层输入。
func TestNextLevelClosureDoesNotBackflow(t *testing.T) {
	s := newTestService(t, testCfg())
	// L1：f1=[10,20]（将成为唯一输入），f2=[30,40]（端点不与 f1 相接）。
	mustAdd(t, s, mf(1, 1, 10, 20, 900))
	mustAdd(t, s, mf(2, 1, 30, 40, 900))
	// L2：g1=[15,25] 与 f1 重叠；g2=[25,30] 与 g1 端点相接（闭合一并纳入）。
	// g2.max=30 == f2.min，但该扩大不得反过来把 f2 拉进本层输入。
	mustAdd(t, s, mf(3, 2, 15, 25, 10))
	mustAdd(t, s, mf(4, 2, 25, 30, 10))
	p := mustPick(t, s)
	if err := eqIDs(p.Inputs, 1); err != nil {
		t.Fatalf("next-level closure must not backflow into source inputs: %v", err)
	}
	if err := eqIDs(p.NextInputs, 3, 4); err != nil {
		t.Fatalf("next-level closure failed: %v", err)
	}
}
