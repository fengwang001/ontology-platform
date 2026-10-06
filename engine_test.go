package ontology

import (
	"errors"
	"sync"
	"testing"
)

func mustErrCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %v，实际成功", want)
	}
	var me *MeterError
	if !errors.As(err, &me) {
		t.Fatalf("错误类型不是 *MeterError: %v", err)
	}
	if me.Code != want {
		t.Fatalf("期望错误类别 %v，实际 %v (%v)", want, me.Code, err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际错误: %v", err)
	}
}

func mustUsage(t *testing.T, e *Engine, point string, from, to, want int64, wantEst bool) {
	t.Helper()
	res, err := e.QueryUsage(point, from, to)
	mustOK(t, err)
	if !res.Usage.IsInt64() || res.Usage.Int64() != want {
		t.Fatalf("查询 [%d,%d] 用电量 = %s，期望 %d", from, to, res.Usage.String(), want)
	}
	if res.ContainsEstimated != wantEst {
		t.Fatalf("查询 [%d,%d] ContainsEstimated=%v，期望 %v", from, to, res.ContainsEstimated, wantEst)
	}
}

func newEngineWithMeter(t *testing.T, id string, digits int, mult, rate int64) (*Engine, *meterState) {
	t.Helper()
	e := NewEngine()
	mustOK(t, e.CreateMeter(Meter{ID: id, Digits: digits, Multiplier: mult, MaxUsagePerUnitTime: rate}))
	return e, e.meters[id]
}

// 换表时刻恰等于旧表最新读数时刻：同值幂等，异值读数冲突；跨表倍率汇总。
func TestReplaceAtLatestReadingTime(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.CreateMeter(Meter{ID: "m1", Digits: 4, Multiplier: 1, MaxUsagePerUnitTime: 100}))
	mustOK(t, e.CreateMeter(Meter{ID: "m2", Digits: 4, Multiplier: 2, MaxUsagePerUnitTime: 100}))
	mustOK(t, e.Attach("P", "m1", 0, 0))
	mustOK(t, e.RegisterReading("m1", 10, 100, Actual))
	mustOK(t, e.ReplaceMeter("P", 10, 100, "m2", 0))
	mustOK(t, e.RegisterReading("m2", 20, 40, Actual))
	mustUsage(t, e, "P", 0, 20, 180, false)
	// 换表衔接点本身不产生用电量
	mustUsage(t, e, "P", 10, 10, 0, false)

	e2 := NewEngine()
	mustOK(t, e2.CreateMeter(Meter{ID: "a", Digits: 4, Multiplier: 1, MaxUsagePerUnitTime: 100}))
	mustOK(t, e2.CreateMeter(Meter{ID: "b", Digits: 4, Multiplier: 1, MaxUsagePerUnitTime: 100}))
	mustOK(t, e2.Attach("P", "a", 0, 0))
	mustOK(t, e2.RegisterReading("a", 5, 50, Actual))
	mustErrCode(t, e2.ReplaceMeter("P", 5, 51, "b", 0), ErrReadingConflict)
}

// 显示值恰等于位数最大值与回零翻转。
func TestMaxValueAndRollover(t *testing.T) {
	e, _ := newEngineWithMeter(t, "m", 3, 1, 100)
	mustOK(t, e.Attach("P", "m", 0, 0))
	mustOK(t, e.RegisterReading("m", 9, 900, Actual))
	mustOK(t, e.RegisterReading("m", 10, 999, Actual)) // 恰好最大值
	mustOK(t, e.RegisterReading("m", 11, 0, Actual))   // 回零
	mustUsage(t, e, "P", 10, 11, 1, false)
	mustUsage(t, e, "P", 9, 11, 100, false)

	// 900->0 一次翻转；插入 5 导致两段都翻转，守恒失败。
	e3, _ := newEngineWithMeter(t, "m", 3, 1, 100)
	mustOK(t, e3.Attach("P", "m", 0, 900))
	mustOK(t, e3.RegisterReading("m", 11, 0, Actual))
	mustErrCode(t, e3.RegisterReading("m", 10, 5, Actual), ErrReadingScheduleConflict)
}

// 合理性上限取等通过，超出拒绝且不改变状态。
func TestRationalLimitEquality(t *testing.T) {
	e, _ := newEngineWithMeter(t, "m", 4, 2, 10)
	mustOK(t, e.Attach("P", "m", 0, 0))
	mustOK(t, e.RegisterReading("m", 5, 25, Actual)) // 2*25=50=10*5
	mustErrCode(t, e.RegisterReading("m", 6, 31, Actual), ErrUnreasonable)
	mustOK(t, e.RegisterReading("m", 6, 30, Actual))
	mustUsage(t, e, "P", 0, 6, 60, false)
}

// 乱序插入：翻转落在前段、翻转落在后段两种接受情形，以及守恒拒绝。
func TestOutOfOrderRolloverSplit(t *testing.T) {
	// 翻转落后段：800->950 不翻，950->100 翻。
	e, _ := newEngineWithMeter(t, "m", 3, 1, 1000)
	mustOK(t, e.Attach("P", "m", 0, 800))
	mustOK(t, e.RegisterReading("m", 10, 100, Actual))
	mustOK(t, e.RegisterReading("m", 5, 950, Actual))
	mustUsage(t, e, "P", 0, 10, 300, false)

	// 翻转落前段：800->50 翻，50->100 不翻。
	e2, _ := newEngineWithMeter(t, "m", 3, 1, 1000)
	mustOK(t, e2.Attach("P", "m", 0, 800))
	mustOK(t, e2.RegisterReading("m", 10, 100, Actual))
	mustOK(t, e2.RegisterReading("m", 5, 50, Actual))
	mustUsage(t, e2, "P", 0, 10, 300, false)

	// 原区间无翻转，插入引入翻转 -> 拒绝。
	e3, _ := newEngineWithMeter(t, "m", 3, 1, 1000)
	mustOK(t, e3.Attach("P", "m", 0, 100))
	mustOK(t, e3.RegisterReading("m", 10, 200, Actual))
	mustErrCode(t, e3.RegisterReading("m", 5, 50, Actual), ErrReadingScheduleConflict)
}

// 估算占位、查询标记、被实抄替换、替换不合理被拒绝、估算删除合并。
func TestEstimatedLifecycle(t *testing.T) {
	// 替换后相邻区间不合理 -> 拒绝替换，估算保留。
	e, _ := newEngineWithMeter(t, "m", 4, 1, 10)
	mustOK(t, e.Attach("P", "m", 0, 0))
	mustOK(t, e.RegisterReading("m", 10, 100, Estimated))
	mustOK(t, e.RegisterReading("m", 11, 102, Actual))
	mustUsage(t, e, "P", 0, 11, 102, true)
	mustErrCode(t, e.RegisterReading("m", 10, 50, Actual), ErrUnreasonable)
	mustUsage(t, e, "P", 0, 11, 102, true) // 估算保留
	mustOK(t, e.RegisterReading("m", 10, 100, Actual))
	mustUsage(t, e, "P", 0, 11, 102, false)
	// 估算不得乱序插入
	mustErrCode(t, e.RegisterReading("m", 5, 5, Estimated), ErrReadingScheduleConflict)

	// 删除估算后合并区间合格 -> 删除成功；实抄不可删除。
	e2, _ := newEngineWithMeter(t, "m", 4, 1, 10)
	mustOK(t, e2.Attach("P", "m", 0, 0))
	mustOK(t, e2.RegisterReading("m", 5, 50, Estimated))
	mustOK(t, e2.RegisterReading("m", 10, 55, Actual))
	mustOK(t, e2.DeleteEstimatedReading("m", 5))
	mustUsage(t, e2, "P", 0, 10, 55, false)
	mustErrCode(t, e2.DeleteEstimatedReading("m", 10), ErrReadingConflict)
	mustErrCode(t, e2.DeleteEstimatedReading("m", 999), ErrNoReading)

	// 删除估算后合并区间：固定倍率下区间表显用电量满足可加性
	// （rawDelta(a,c)==rawDelta(a,b)+rawDelta(b,c)，无论 b 落在翻转的哪一侧），
	// 而合并后时长不变，故“两段均合格”时合并区间数学上必合格；拒绝删除的
	// “不合理”分支只能在历史脏状态下出现。这里验证正常路径：删除后合并重算。
	e3, _ := newEngineWithMeter(t, "m", 4, 1, 20)
	mustOK(t, e3.Attach("P", "m", 0, 0))
	mustOK(t, e3.RegisterReading("m", 5, 50, Estimated)) // 50 <= 100
	mustOK(t, e3.RegisterReading("m", 10, 100, Actual))  // 50 <= 100
	mustOK(t, e3.DeleteEstimatedReading("m", 5))
	mustUsage(t, e3, "P", 0, 10, 100, false)
}

// 跨多只电表、不同倍率的查询与三点可加性。
func TestCrossMeterQueryAndAdditivity(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.CreateMeter(Meter{ID: "m1", Digits: 4, Multiplier: 3, MaxUsagePerUnitTime: 10000}))
	mustOK(t, e.CreateMeter(Meter{ID: "m2", Digits: 4, Multiplier: 2, MaxUsagePerUnitTime: 10000}))
	mustOK(t, e.Attach("P", "m1", 0, 0))
	mustOK(t, e.RegisterReading("m1", 5, 10, Actual))
	mustOK(t, e.ReplaceMeter("P", 10, 30, "m2", 0))
	mustOK(t, e.RegisterReading("m2", 15, 20, Actual))
	mustOK(t, e.RegisterReading("m2", 20, 70, Estimated))
	// 旧表：30*3=90；新表：((20-0)+(70-20))*2=70*2=140
	mustUsage(t, e, "P", 0, 20, 230, true)
	mustUsage(t, e, "P", 0, 10, 90, false)
	mustUsage(t, e, "P", 10, 20, 140, true)
	// 三点可加性：U(0,20)=U(0,15)+U(15,20)
	a := queryVal(t, e, "P", 0, 15)
	b := queryVal(t, e, "P", 15, 20)
	c := queryVal(t, e, "P", 0, 20)
	if a+b != c {
		t.Fatalf("可加性失败: %d + %d != %d", a, b, c)
	}
	// 无读数
	_, err := e.QueryUsage("P", 1, 20)
	mustErrCode(t, err, ErrNoReading)
}

func queryVal(t *testing.T, e *Engine, point string, from, to int64) int64 {
	t.Helper()
	r, err := e.QueryUsage(point, from, to)
	mustOK(t, err)
	if !r.Usage.IsInt64() {
		t.Fatalf("用电量超出 int64")
	}
	return r.Usage.Int64()
}

// 拒绝次序：参数非法 > 不在挂接期内 > 与已有读数冲突 > 读数冲突 > 不合理 > 无读数。
func TestRejectionOrder(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.CreateMeter(Meter{ID: "m", Digits: 3, Multiplier: 1, MaxUsagePerUnitTime: 10}))
	mustOK(t, e.Attach("P", "m", 10, 0))
	// 超位显示值（参数非法）优先于一切
	mustErrCode(t, e.RegisterReading("m", 5, 1000, Actual), ErrInvalidArgument)
	// 时刻早于安装（不在挂接期内）优先于其他
	mustErrCode(t, e.RegisterReading("m", 5, 5, Actual), ErrNotAttached)
	// 同刻异值实抄：读数冲突（优先于合理性）
	mustOK(t, e.RegisterReading("m", 10, 0, Actual)) // 幂等同值
	mustErrCode(t, e.RegisterReading("m", 10, 1, Actual), ErrReadingConflict)
	// 乱序估算：与已有读数冲突（先于合理性判定）
	mustOK(t, e.RegisterReading("m", 20, 100, Actual))
	mustErrCode(t, e.RegisterReading("m", 15, 999, Estimated), ErrReadingScheduleConflict)
	// 乱序实抄：翻转守恒冲突优先于合理性
	mustErrCode(t, e.RegisterReading("m", 15, 150, Actual), ErrReadingScheduleConflict)
	mustOK(t, e.RegisterReading("m", 15, 50, Actual))
	mustErrCode(t, e.RegisterReading("m", 18, 99, Actual), ErrUnreasonable) // 50->99=49 > 30
	// 无读数排在最后
	_, err := e.QueryUsage("P", 11, 12)
	mustErrCode(t, err, ErrNoReading)
}

// 同刻替换与删除的原子性：拒绝操作不改变任何状态。
func TestRejectionIsAtomic(t *testing.T) {
	e, m := newEngineWithMeter(t, "m", 3, 1, 100)
	mustOK(t, e.Attach("P", "m", 0, 0))
	mustOK(t, e.RegisterReading("m", 5, 10, Estimated))
	before := m.tree.len()
	mustErrCode(t, e.RegisterReading("m", 5, 1000, Actual), ErrInvalidArgument) // 超位（3 位）
	mustErrCode(t, e.RegisterReading("m", 5, 600, Actual), ErrUnreasonable)     // 替换超标：0->600 > 100*5=500
	if m.tree.len() != before {
		t.Fatalf("被拒绝操作改变了读数数量: %d -> %d", before, m.tree.len())
	}
	r, ok := m.tree.get(5)
	if !ok || r.Kind != Estimated || r.Value != 10 {
		t.Fatalf("被拒绝替换后估算读数未保留: %+v %v", r, ok)
	}
}

// 并发调用等价于某种串行顺序：并发混合同刻幂等写与查询，不 panic、结果确定。
func TestConcurrentAccess(t *testing.T) {
	e, _ := newEngineWithMeter(t, "m", 4, 1, 100000)
	mustOK(t, e.Attach("P", "m", 0, 0))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := int64(1); k <= 200; k++ {
				_ = e.RegisterReading("m", k, uint64(k), Actual)
				_, _ = e.QueryUsage("P", 0, k)
			}
		}()
	}
	wg.Wait()
	mustUsage(t, e, "P", 0, 200, 200, false)
}

// 同一只电表拆除后在同一供电点重新挂接：两个 tour 的读数互不串扰，
// 乱序插入不能跨 tour 取相邻读数。
func TestMeterReattachMultipleTours(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.CreateMeter(Meter{ID: "m1", Digits: 4, Multiplier: 1, MaxUsagePerUnitTime: 10000}))
	mustOK(t, e.CreateMeter(Meter{ID: "m2", Digits: 4, Multiplier: 1, MaxUsagePerUnitTime: 10000}))
	mustOK(t, e.CreateMeter(Meter{ID: "m3", Digits: 4, Multiplier: 1, MaxUsagePerUnitTime: 10000}))
	mustOK(t, e.CreateMeter(Meter{ID: "m4", Digits: 4, Multiplier: 1, MaxUsagePerUnitTime: 10000}))
	mustOK(t, e.Attach("P", "m1", 0, 0))
	mustOK(t, e.RegisterReading("m1", 5, 50, Actual))
	mustOK(t, e.ReplaceMeter("P", 10, 100, "m2", 0))
	mustOK(t, e.RegisterReading("m2", 15, 60, Actual))
	// m2 拆除后，把旧表 m1 在时刻 20 重新挂接（新 tour）
	mustOK(t, e.ReplaceMeter("P", 20, 80, "m3", 0))
	mustOK(t, e.RegisterReading("m3", 25, 30, Actual))
	// 此时点处于活动状态；先拆掉 m3 再重新挂 m1
	mustOK(t, e.ReplaceMeter("P", 30, 40, "m4", 0))
	mustOK(t, e.ReplaceMeter("P", 35, 45, "m1", 100)) // m1 重新挂接，初始读数 100
	// 第二个 tour：安装 35@100，追加读数
	mustOK(t, e.RegisterReading("m1", 40, 120, Actual))
	// 乱序插入到第二 tour 内（35..40 之间），前驱只能是 35@100，不能取到第一 tour 的 10@100
	mustOK(t, e.RegisterReading("m1", 38, 110, Actual))
	mustUsage(t, e, "P", 35, 40, 20, false)
	// 对已拆除 tour 中间时刻登记 -> 不在挂接期内
	mustErrCode(t, e.RegisterReading("m1", 7, 60, Actual), ErrNotAttached)
	// 跨多 tour 的查询：第一 tour 段与第二 tour 段分别计算
	mustUsage(t, e, "P", 0, 40,
		100 /*m1 tour1: 0..50..100*/ +80 /*m2: 0..60..80*/ +40 /*m3: 0..30..40*/ +65 /*m1 tour2: 100..110..120*/, false)
}

// 复杂度可验证：单表登记/查询耗时不随后续读数总数增长（微基准，仅断言量级）。
func TestComplexityDoesNotGrow(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	bench := func(n int) float64 {
		e := NewEngine()
		mustOK(t, e.CreateMeter(Meter{ID: "m", Digits: 6, Multiplier: 1, MaxUsagePerUnitTime: 1_000_000}))
		mustOK(t, e.Attach("P", "m", 0, 0))
		for i := int64(1); i <= int64(n); i++ {
			mustOK(t, e.RegisterReading("m", i*2, uint64(i), Actual))
		}
		start := testing.AllocsPerRun(100, func() {
			_, _ = e.QueryUsage("P", 100, 102)
		})
		return start
	}
	a1000 := bench(1000)
	a4000 := bench(4000)
	// 区间查询只命中两棵边界路径 + 整含子树缓存；分配数不应随总量 4 倍而显著增长。
	if a4000 > a1000*2+4 {
		t.Fatalf("查询分配随读数总数增长: n=1000 -> %.1f, n=4000 -> %.1f", a1000, a4000)
	}
}
