package toollife_test

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"ontology/toollife"
)

// warmHistory 用 historyCount 条「申请->记账」流水把全局申请台账撑大，
// 这些申请全部落在与待测刀组不同的刀组上，因此只影响历史长度、不影响组内刀具数。
func warmHistory(tb testing.TB, s *toollife.Service, historyCount int) {
	tb.Helper()
	gid := "history"
	cfg := strictCfg(1 << 62)
	mustCode(tb, s.Magazine().AddGroup(gid, cfg), 0)
	mustCode(tb, s.Magazine().AddTool(gid, "h0"), 0)
	for i := 0; i < historyCount; i++ {
		rid := fmt.Sprintf("h-%d", i)
		okApply(tb, s, rid, gid, 1)
		mustCode(tb, s.Settle(rid, 1), 0)
	}
}

// benchApplyAtHistory 固定刀组刀具数（8），在两档历史长度下测单次申请开销。
func benchApplyAtHistory(b *testing.B, historyCount int) {
	const target = "target"
	setup := func() *toollife.Service {
		s := toollife.NewService()
		mustCode(b, s.Magazine().AddGroup(target, strictCfg(1<<62)), 0)
		for i := 0; i < 8; i++ {
			mustCode(b, s.Magazine().AddTool(target, fmt.Sprintf("k%d", i)), 0)
		}
		warmHistory(b, s, historyCount)
		return s
	}
	s := setup()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rid := fmt.Sprintf("probe-%d", i)
		_, err := s.Apply(rid, target, 1)
		mustCode(b, err, 0)
		// 立即记账，保持目标刀上预占量恒定，避免刀被占满影响选刀路径。
		mustCode(b, s.Settle(rid, 1), 0)
	}
}

func BenchmarkApplyHistory1k(b *testing.B)   { benchApplyAtHistory(b, 1_000) }
func BenchmarkApplyHistory100k(b *testing.B) { benchApplyAtHistory(b, 100_000) }

// TestSelectionCostIndependentOfHistory 以两档历史长度（1k vs 100k）
// 各做同数量的申请+记账，断言耗时比值有界。这不是微基准的绝对数字，
// 而是「选刀开销只能与刀组内刀具数相关、不随历史申请总数增长」的可验证证据：
// 若选刀扫描了历史台账，100x 的历史增长必然带来显著超线性的时间增长。
func TestSelectionCostIndependentOfHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cost test in -short mode")
	}
	const probes = 20_000

	measure := func(history int) int64 {
		s := toollife.NewService()
		mustCode(t, s.Magazine().AddGroup("target", strictCfg(1<<30)), 0)
		for i := 0; i < 8; i++ {
			mustCode(t, s.Magazine().AddTool("target", fmt.Sprintf("k%d", i)), 0)
		}
		warmHistory(t, s, history)

		runtime.GC()
		start := time.Now()
		for i := 0; i < probes; i++ {
			rid := fmt.Sprintf("probe-%d", i)
			okApply(t, s, rid, "target", 1)
			mustCode(t, s.Settle(rid, 1), 0)
		}
		elapsed := time.Since(start).Nanoseconds()
		t.Logf("history=%7d probes=%d total=%10dns per-probe=%4dns",
			history, probes, elapsed, elapsed/probes)
		return elapsed
	}

	_ = measure(1_000) // 预热
	small := measure(1_000)
	large := measure(100_000)
	ratio := float64(large) / float64(small)
	t.Logf("history 100x -> time ratio %.2fx (bounded threshold 3.0x)", ratio)
	if ratio > 3.0 {
		t.Fatalf("selection cost appears to grow with history: ratio %.2fx", ratio)
	}
}

// TestSelectionCostGrowsWithToolCount 作为正向对照：刀具数增长时开销应随之增长，
// 证明上一测试度量的确实是选刀路径而非无关常量。
func TestSelectionCostGrowsWithToolCount(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cost test in -short mode")
	}
	const probes = 3_000
	// 让组内所有刀都处于「预占占满」状态：每次申请都必须扫描整组，
	// 最终返回暂无余量/无刀，选刀路径成本真实正比于刀具数。
	measure := func(toolCount int) int64 {
		s := toollife.NewService()
		mustCode(t, s.Magazine().AddGroup("g", strictCfg(1<<62)), 0)
		for i := 0; i < toolCount; i++ {
			mustCode(t, s.Magazine().AddTool("g", fmt.Sprintf("t%d", i)), 0)
		}
		for i := 0; i < toolCount-1; i++ {
			okApply(t, s, fmt.Sprintf("fill-%d", i), "g", 1<<62-1)
		}
		// 最后一把保持空闲，但超大预计（>寿命上限）使任何刀都无法承载，
		// 每次探针都扫描完全部刀后返回无刀 -> 扫描长度 = toolCount。
		start := time.Now()
		for i := 0; i < probes; i++ {
			_, err := s.Apply(fmt.Sprintf("p-%d", i), "g", 1<<62+1)
			mustCode(t, err, toollife.ErrNoTool)
		}
		elapsed := time.Since(start).Nanoseconds()
		t.Logf("tools=%4d per-probe=%5dns", toolCount, elapsed/probes)
		return elapsed
	}
	fast := measure(16)
	slow := measure(4096)
	ratio := float64(slow) / float64(fast)
	t.Logf("tool count 256x -> time ratio %.2fx (expect > 3x)", ratio)
	if ratio <= 3.0 {
		t.Fatalf("expected cost to grow with tool count, got ratio %.2fx", ratio)
	}
}
