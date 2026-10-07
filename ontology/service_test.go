package ontology

import "testing"

// TestErrorOrdering 验证拒绝次序：参数非法 > 起点超限 > 终点超限，只报第一个。
func TestErrorOrdering(t *testing.T) {
	s, _ := mustRegister(t)

	// 先把 s1 的起点名额用掉 2 个（LT1 src cap=2），t1 终点名额用掉 1 个。
	mustOK(t, s.CreateLink("LT1", Pair{"s1", "t1"}))
	mustOK(t, s.CreateLink("LT1", Pair{"s1", "t2"}))

	// 同时触发参数非法（实例不存在）与起点已满：必须先报参数非法。
	err := s.CreateLink("LT1", Pair{"ghost", "t3"})
	if KindOf(err) != KindInvalidArgument {
		t.Fatalf("期望 INVALID_ARGUMENT，得到 %v", err)
	}
	t.Logf("输入=LT1(ghost->t3) 实际输出=%s 依据=实例不存在优先于任何基数判定", KindOf(err))

	// 起点已满载，终点空闲：报起点超限。
	err = s.CreateLink("LT1", Pair{"s1", "t3"})
	if KindOf(err) != KindSourceCardinality {
		t.Fatalf("期望 SOURCE_CARDINALITY，得到 %v", err)
	}
	t.Logf("输入=LT1(s1->t3) 实际输出=%s 依据=s1 起点已占2=上限", KindOf(err))

	// 起点空闲（s2 占0），终点已满（t1 占1=上限1）：报终点超限，证明两端独立判定。
	err = s.CreateLink("LT1", Pair{"s2", "t1"})
	if KindOf(err) != KindTargetCardinality {
		t.Fatalf("期望 TARGET_CARDINALITY，得到 %v", err)
	}
	t.Logf("输入=LT1(s2->t1) 实际输出=%s 依据=t1 终点已占1=上限", KindOf(err))
}

// TestBoundaryExactAndOverByOne 覆盖「恰好等于上限合法、再多一个拒绝」边界。
func TestBoundaryExactAndOverByOne(t *testing.T) {
	s, _ := mustRegister(t)

	mustOK(t, s.CreateLink("LT2", Pair{"u1", "v1"})) // ExactlyOne：占用恰好等于上限1
	t.Logf("输入=LT2(u1->v1) 实际输出=ACCEPTED 依据=占用1恰好等于上限1")

	err := s.CreateLink("LT2", Pair{"u1", "v2"}) // 超出一个
	if KindOf(err) != KindSourceCardinality {
		t.Fatalf("期望起点超限，得到 %v", err)
	}
	t.Logf("输入=LT2(u1->v2) 实际输出=%s 依据=占用将变为2>1", KindOf(err))

	// 不同有序对方向独立：u2 未占用，仍可连接。
	mustOK(t, s.CreateLink("LT2", Pair{"u2", "v1"}))
}

// TestBatchCumulativeRejectsLater 是题目点名场景：后序条目因批内累积被拒，
// 而它在批次前快照下本应独立合法。
func TestBatchCumulativeRejectsLater(t *testing.T) {
	s, _ := mustRegister(t)

	items := []BatchItem{
		{LinkType: "LT1", Pair: Pair{"s1", "t1"}},
		{LinkType: "LT1", Pair: Pair{"s1", "t2"}},
		{LinkType: "LT1", Pair: Pair{"s2", "t1"}}, // 独立看合法；批内 t1 已满
	}
	t.Logf("批量输入=%v", fmtItems(items))

	res := s.ImportLinks(BestEffort, items)
	t.Logf("尽力而为输出=%v", fmtResults(res))

	want := []ErrKind{KindOK, KindOK, KindTargetCardinality}
	for i, w := range want {
		if res.Results[i].Kind != w {
			t.Fatalf("条目%d 期望 %s，得到 %s", i, w, res.Results[i].Kind)
		}
	}
	if got := s.UsedTarget("LT1", "t1"); got != 1 {
		t.Fatalf("被拒条目不得占用基数：t1 占用应=1，得到 %d", got)
	}
	t.Logf("依据=前两条批内累积使 t1 终点占用达上限1，第三条被拒且不留占用")
}

// TestTwoSemanticsDiffer 同一输入下全有或全无 vs 尽力而为的结果差异。
func TestTwoSemanticsDiffer(t *testing.T) {
	items := []BatchItem{
		{LinkType: "LT1", Pair: Pair{"s1", "t1"}},
		{LinkType: "LT1", Pair: Pair{"s1", "t2"}},
		{LinkType: "LT1", Pair: Pair{"s2", "t1"}}, // 终点满
		{LinkType: "LT1", Pair: Pair{"s3", "t3"}}, // 自身合法，但 AON 下不会执行
	}
	t.Logf("输入=%v", fmtItems(items))

	// 全有或全无
	sAll, _ := mustRegister(t)
	rAll := sAll.ImportLinks(AllOrNothing, items)
	t.Logf("全有或全无输出=%v aborted=%v abortIndex=%d", fmtResults(rAll), rAll.Aborted, rAll.AbortIndex)
	if !rAll.Aborted || rAll.AbortIndex != 2 {
		t.Fatalf("应在下标2中止")
	}
	if len(sAll.Snapshot()) != 0 {
		t.Fatalf("全有或全无失败后存储必须为空，得到 %v", sAll.Snapshot())
	}
	if sAll.UsedSource("LT1", "s1") != 0 {
		t.Fatalf("回滚后账本必须回到批次前：s1 占用应=0")
	}

	// 尽力而为
	sBest, _ := mustRegister(t)
	rBest := sBest.ImportLinks(BestEffort, items)
	t.Logf("尽力而为输出=%v", fmtResults(rBest))
	want := []ErrKind{KindOK, KindOK, KindTargetCardinality, KindOK}
	for i, w := range want {
		if rBest.Results[i].Kind != w {
			t.Fatalf("条目%d 期望 %s，得到 %s", i, w, rBest.Results[i].Kind)
		}
	}
	if len(sBest.Snapshot()) != 3 {
		t.Fatalf("尽力而为应留下3条，得到 %d", len(sBest.Snapshot()))
	}
}

// TestBatchDuplicatePairs 批内有序对重复属于参数非法；与已存在链接重复同样非法。
func TestBatchDuplicatePairs(t *testing.T) {
	s, _ := mustRegister(t)
	mustOK(t, s.CreateLink("LT1", Pair{"s1", "t1"}))

	items := []BatchItem{
		{LinkType: "LT1", Pair: Pair{"s2", "t2"}},
		{LinkType: "LT1", Pair: Pair{"s2", "t2"}}, // 批内重复
		{LinkType: "LT1", Pair: Pair{"s1", "t1"}}, // 与已存储重复
	}
	res := s.ImportLinks(BestEffort, items)
	for i, want := range []ErrKind{KindOK, KindInvalidArgument, KindInvalidArgument} {
		if res.Results[i].Kind != want {
			t.Fatalf("条目%d 期望 %s，得到 %s", i, want, res.Results[i].Kind)
		}
	}
	t.Logf("输入=%v 输出=%v 依据=批内重复/存储重复均归一化为参数非法", fmtItems(items), fmtResults(res))
}

// TestDeleteReleasesImmediately 删除释放后名额立即可被同线程后续创建占用；
// 删除不存在链接报 NOT_FOUND，与基数已满区分。
func TestDeleteReleasesImmediately(t *testing.T) {
	s, _ := mustRegister(t)
	mustOK(t, s.CreateLink("LT2", Pair{"u1", "v1"}))

	if err := s.DeleteLink("LT2", Pair{"u1", "v1"}); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if err := s.CreateLink("LT2", Pair{"u1", "v2"}); err != nil {
		t.Fatalf("删除释放后应立即能创建，得到 %v", err)
	}
	t.Logf("删除 LT2(u1->v1) 后创建 LT2(u1->v2)=ACCEPTED 依据=释放与删除同一临界区生效")

	err := s.DeleteLink("LT2", Pair{"u1", "v1"})
	if KindOf(err) != KindNotFound {
		t.Fatalf("不存在链接删除应 NOT_FOUND，得到 %v", err)
	}

	// 对照：存在但基数已满是另一种类别。
	err = s.CreateLink("LT2", Pair{"u1", "v3"})
	if KindOf(err) != KindSourceCardinality {
		t.Fatalf("应区分基数已满，得到 %v", err)
	}
	t.Logf("NOT_FOUND 与 SOURCE_CARDINALITY_EXCEEDED 是两类不同错误")
}

// TestIndependentSides 一端超限不影响对另一端结论的产出（内部两端都判定），
// 且只报优先级更高的一端（起点先于终点）。
func TestIndependentSides(t *testing.T) {
	s, _ := mustRegister(t)
	mustOK(t, s.CreateLink("LT1", Pair{"s1", "t1"}))
	mustOK(t, s.CreateLink("LT1", Pair{"s2", "t2"}))
	mustOK(t, s.CreateLink("LT1", Pair{"s2", "t3"})) // s2 起点占满(2)；t1 终点也已被第一条占满(1)

	// 有序对 (s2,t1) 尚不存在，但 s2 起点与 t1 终点同时已满：两端独立判定，
	// 对外只报次序更前的起点超限。
	err := s.CreateLink("LT1", Pair{"s2", "t1"})
	if KindOf(err) != KindSourceCardinality {
		t.Fatalf("两端同满时应报起点一侧，得到 %v", err)
	}
	t.Logf("输入=LT1(s2->t1) 输出=%s 依据=两端内部都独立判定，输出按起点优先", KindOf(err))
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，得到 %v", err)
	}
}
