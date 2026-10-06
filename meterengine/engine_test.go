package meterengine

import "testing"

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: 意外错误 %v", ctx, err)
	}
}

func errClass(err error) ErrorClass {
	if ee, ok := err.(*EngineError); ok {
		return ee.Class
	}
	return 0
}

func mustErr(t *testing.T, err error, class ErrorClass, ctx string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: 期望错误类别 %d，实际成功", ctx, class)
	}
	if errClass(err) != class {
		t.Fatalf("%s: 期望错误类别 %d，实际 %d(%v)", ctx, class, errClass(err), err)
	}
}

func TestBasicSegmentAndMultiplier(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.SetPointLimit("p", 100), "上限")
	mustOK(t, e.RegisterMeter("m1", 3, 2), "登记")
	mustOK(t, e.InstallMeter("p", "m1", 1, 0), "安装")
	mustOK(t, e.RegisterReading("m1", 3, 60, Actual), "读数")
	got, err := e.Query("p", 1, 3)
	mustOK(t, err, "查询")
	if got.Energy != 120 || got.HasEstimated {
		t.Fatalf("得到 %+v，期望 120/无估算", got)
	}
}

func TestRolloverMaxAndZero(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.SetPointLimit("p", 1000), "上限")
	mustOK(t, e.RegisterMeter("m1", 3, 1), "登记")
	mustOK(t, e.InstallMeter("p", "m1", 1, 990), "安装")
	mustOK(t, e.RegisterReading("m1", 2, 999, Actual), "最大值")
	mustOK(t, e.RegisterReading("m1", 3, 0, Actual), "回零")
	got, err := e.Query("p", 1, 3)
	mustOK(t, err, "查询")
	if got.Energy != 10 {
		t.Fatalf("翻转后用电量 %d，期望 10", got.Energy)
	}
}

func TestReasonableLimitEqualPasses(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.SetPointLimit("p", 100), "上限")
	mustOK(t, e.RegisterMeter("m1", 4, 1), "登记")
	mustOK(t, e.InstallMeter("p", "m1", 1, 0), "安装")
	mustOK(t, e.RegisterReading("m1", 6, 500, Actual), "取等通过")
	mustErr(t, e.RegisterReading("m1", 7, 601, Actual), ClassUnreasonable, "超出上限")
}

func TestSwapAtLatestReadingTimeAndCrossMeterQuery(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.SetPointLimit("p", 1000), "上限")
	mustOK(t, e.RegisterMeter("old", 3, 2), "旧表")
	mustOK(t, e.RegisterMeter("new", 3, 3), "新表")
	mustOK(t, e.InstallMeter("p", "old", 1, 10), "安装旧表")
	mustOK(t, e.RegisterReading("old", 5, 20, Actual), "旧表末次")
	mustOK(t, e.SwapMeter("p", 5, 20, "new", 5), "换表时刻=最新读数")
	mustOK(t, e.RegisterReading("new", 7, 15, Actual), "新表读数")
	got, err := e.Query("p", 1, 7)
	mustOK(t, err, "查询")
	if got.Energy != 50 { // (20-10)*2 + (15-5)*3
		t.Fatalf("跨表用电 %d，期望 50", got.Energy)
	}
	// 换表时刻自身查询不产生用电量。
	zero, err := e.Query("p", 5, 5)
	mustOK(t, err, "换表时刻查询")
	if zero.Energy != 0 {
		t.Fatalf("换表时刻用电 %d，期望 0", zero.Energy)
	}
}

func TestSwapConflictAndOrdering(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.SetPointLimit("p", 1000), "上限")
	mustOK(t, e.RegisterMeter("old", 3, 1), "旧表")
	mustOK(t, e.RegisterMeter("new", 3, 1), "新表")
	mustOK(t, e.InstallMeter("p", "old", 10, 0), "安装")
	mustOK(t, e.RegisterReading("old", 20, 10, Actual), "读数")
	mustErr(t, e.SwapMeter("p", 15, 0, "new", 0), ClassConflict, "早于最新读数")
	mustErr(t, e.SwapMeter("p", 15, 10000, "new", 0), ClassInvalidArgument, "参数非法优先")
	mustErr(t, e.SwapMeter("p", 20, 11, "new", 0), ClassReadingConflict, "末次值不一致")
	// 换表成功后旧表不可再登记。
	mustOK(t, e.SwapMeter("p", 20, 10, "new", 0), "换表")
	mustErr(t, e.RegisterReading("old", 20, 9, Actual), ClassNotAttached, "旧表已拆除")
}

func TestOutOfOrderRolloverTwoCasesAndConservation(t *testing.T) {
	setup := func(t *testing.T) *Engine {
		e := NewEngine()
		mustOK(t, e.SetPointLimit("p", 1000), "上限")
		mustOK(t, e.RegisterMeter("m", 3, 1), "登记")
		mustOK(t, e.InstallMeter("p", "m", 1, 900), "安装")
		mustOK(t, e.RegisterReading("m", 4, 100, Actual), "翻转后读数")
		return e
	}
	e := setup(t)
	mustOK(t, e.RegisterReading("m", 2, 950, Actual), "翻转落在前段")
	got, _ := e.Query("p", 1, 4)
	if got.Energy != 200 {
		t.Fatalf("情形一 %d，期望 200", got.Energy)
	}
	e = setup(t)
	mustOK(t, e.RegisterReading("m", 2, 50, Actual), "翻转落在后段")
	got, _ = e.Query("p", 1, 4)
	if got.Energy != 200 {
		t.Fatalf("情形二 %d，期望 200", got.Energy)
	}
	e = setup(t)
	mustErr(t, e.RegisterReading("m", 2, 500, Actual), ClassConflict, "翻转不守恒")
	e = setup(t)
	mustErr(t, e.RegisterReading("m", 2, 950, Estimated), ClassConflict, "估算不得乱序")
}

func TestEstimatedReplaceAndDelete(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.SetPointLimit("p", 100), "上限")
	mustOK(t, e.RegisterMeter("m", 4, 1), "登记")
	mustOK(t, e.InstallMeter("p", "m", 1, 0), "安装")
	mustOK(t, e.RegisterReading("m", 3, 100, Estimated), "估算占位")
	mustOK(t, e.RegisterReading("m", 5, 200, Actual), "实抄")
	got, _ := e.Query("p", 1, 5)
	if !got.HasEstimated || got.Energy != 200 {
		t.Fatalf("估算参与 %+v", got)
	}
	// 替换为 250：前段 0->250 跨 2 单位，上限 200，拒绝且估算保留。
	mustErr(t, e.ReplaceEstimated("m", 3, 250), ClassUnreasonable, "替换后不合理")
	r := e.trees[e.meters["m"]].find(3)
	if r.kind != Estimated || r.display != 100 {
		t.Fatalf("估算未保留: %+v", r)
	}
	mustOK(t, e.ReplaceEstimated("m", 3, 150), "合法替换")
	got, _ = e.Query("p", 1, 5)
	if got.HasEstimated || got.Energy != 200 {
		t.Fatalf("替换后 %+v，期望 200/无估算", got)
	}
	// 删除估算路径单独验证。
	e2 := NewEngine()
	mustOK(t, e2.SetPointLimit("p", 100), "上限")
	mustOK(t, e2.RegisterMeter("m", 4, 1), "登记")
	mustOK(t, e2.InstallMeter("p", "m", 1, 0), "安装")
	mustOK(t, e2.RegisterReading("m", 3, 100, Estimated), "估算")
	mustOK(t, e2.RegisterReading("m", 5, 200, Actual), "实抄")
	mustOK(t, e2.DeleteEstimated("m", 3), "删除估算并合并")
	got, _ = e2.Query("p", 1, 5)
	if got.Energy != 200 || got.HasEstimated {
		t.Fatalf("删除后 %+v", got)
	}
	mustErr(t, e2.DeleteEstimated("m", 3), ClassNoReading, "已删除")
	mustErr(t, e2.DeleteEstimated("m", 5), ClassConflict, "实抄不可删")
}

func TestDeleteEstimatedRejectedOnMergedSegment(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.SetPointLimit("p", 100), "初始上限")
	mustOK(t, e.RegisterMeter("m", 4, 1), "登记")
	mustOK(t, e.InstallMeter("p", "m", 1, 0), "安装")
	mustOK(t, e.RegisterReading("m", 3, 100, Estimated), "估算")
	mustOK(t, e.RegisterReading("m", 4, 200, Actual), "实抄")
	// 事后收紧上限：合并区间 0->200 跨 3 单位，新上限 3*60=180，删除被拒绝。
	mustOK(t, e.SetPointLimit("p", 60), "收紧上限")
	mustErr(t, e.DeleteEstimated("m", 3), ClassUnreasonable, "合并后超限")
}

func TestNoReadingAndNotAttached(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.SetPointLimit("p", 1000), "上限")
	mustOK(t, e.RegisterMeter("m", 3, 1), "登记")
	mustOK(t, e.RegisterMeter("m2", 3, 1), "新表")
	mustOK(t, e.InstallMeter("p", "m", 10, 0), "安装")
	mustOK(t, e.RegisterReading("m", 20, 5, Actual), "读数")
	mustOK(t, e.SwapMeter("p", 30, 8, "m2", 0), "换表")
	_, err := e.Query("p", 10, 21)
	mustErr(t, err, ClassNoReading, "中间时刻无读数")
	got, err := e.Query("p", 10, 30)
	mustOK(t, err, "换表时刻可作端点")
	if got.Energy != 8 {
		t.Fatalf("到换表时刻用电 %d，期望 8", got.Energy)
	}
}

func TestRejectOrdering(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.SetPointLimit("p", 1000), "上限")
	mustOK(t, e.RegisterMeter("m", 3, 1), "登记")
	// 参数非法：未知电表（同时也是空/非空检查）先于挂接期。
	mustErr(t, e.RegisterReading("nope", 1, 0, Actual), ClassInvalidArgument, "未知电表")
	mustOK(t, e.InstallMeter("p", "m", 10, 0), "安装")
	// 不在挂接期：时刻合法、显示值也合法，但早于安装时刻。
	mustErr(t, e.RegisterReading("m", 5, 0, Actual), ClassNotAttached, "早于安装时刻")
	// 参数非法优先：即便时刻不在挂接期，显示值超位数仍先报参数非法。
	mustErr(t, e.RegisterReading("m", 5, 9999, Actual), ClassInvalidArgument, "参数非法先于挂接期")
	// 显示值非法（在挂接期内）先于其他类别。
	mustOK(t, e.RegisterReading("m", 20, 10, Estimated), "估算")
	mustErr(t, e.RegisterReading("m", 20, 9999, Estimated), ClassInvalidArgument, "参数非法先于读数冲突")
	mustErr(t, e.RegisterReading("m", 20, 11, Estimated), ClassReadingConflict, "估算+估算")
}

func TestThreePointAdditivityAcrossSwaps(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.SetPointLimit("p", 1000), "上限")
	mustOK(t, e.RegisterMeter("a", 3, 2), "a")
	mustOK(t, e.RegisterMeter("b", 4, 3), "b")
	mustOK(t, e.InstallMeter("p", "a", 1, 100), "装 a")
	mustOK(t, e.RegisterReading("a", 3, 150, Actual), "a 读数")
	mustOK(t, e.SwapMeter("p", 5, 200, "b", 50), "换 b")
	mustOK(t, e.RegisterReading("b", 7, 70, Estimated), "b 估算")
	mustOK(t, e.RegisterReading("b", 9, 90, Actual), "b 实抄")
	q := func(from, to int64) int64 {
		r, err := e.Query("p", from, to)
		mustOK(t, err, "查询")
		return r.Energy
	}
	whole := q(1, 9)
	mid := q(1, 5) + q(5, 9)
	if whole != mid {
		t.Fatalf("首到末 %d != 首到换表+换表到末 %d", whole, mid)
	}
	if q(1, 3)+q(3, 9) != whole {
		t.Fatal("三点可加性失败")
	}
	if whole != (100*2)+(40*3) { // a:100*2, b:40*3
		t.Fatalf("总用电 %d，期望 320", whole)
	}
}
