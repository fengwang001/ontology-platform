package ontology

import (
	"fmt"
	"testing"
)

// buildHistory 在单张保单上制造 n 笔已受理历史，年度跨度随 n 增长。
// 调用方随后测量的结算路径只读取目标年度的 O(1) 累计，不遍历这些历史。
func buildHistory(b *testing.B, n int) (*Engine, Claim) {
	b.Helper()
	spec := PolicySpec{
		InceptDay:          0,
		YearLen:            11,
		PerClaimDeductible: 30,
		AnnualDeductCap:    1_000_000, // 足够大，免赔每年重置即可
		InpatientRate:      80,
		OutpatientRate:     60,
		OOPCap:             1_000_000, // 历史年度不封顶
		ExcludedCodes:      map[string]bool{"EX": true},
	}
	e := NewEngine()
	if err := e.RegisterPolicy("p", spec); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		day := int64(i) // 每年 11 天 => 年度数随 n 线性增长
		id := fmt.Sprintf("H%d", i)
		c := Claim{ID: id, AccDay: day, Lines: []Line{
			{Code: "A", Category: CatInpatient, Amount: 50},
			{Code: "B", Category: CatOutpatient, Amount: 40},
		}}
		if _, err := e.Submit("p", c); err != nil {
			b.Fatal(err)
		}
	}
	// 新事故日落在全新年度（全新累计），保证触发完整免赔/比例路径。
	probe := Claim{ID: "PROBE", AccDay: int64(n + 50), Lines: []Line{
		{Code: "A", Category: CatInpatient, Amount: 123},
		{Code: "EX", Category: CatInpatient, Amount: 77},
		{Code: "B", Category: CatOutpatient, Amount: 89},
	}}
	return e, probe
}

func benchmarkSettlePath(b *testing.B, n int) {
	e, probe := buildHistory(b, n)
	spec := e.policies["p"].spec
	// 计时：纯结算函数，输入为“全新年度”的零累计；
	// 以及引擎在该独立年度上的一次提交（含 map/锁/记账的常数开销）。
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = settle(probe, spec, 0, 0)
	}
}

// BenchmarkSubmitSmall：历史 1_000 笔 / 约 91 个年度。
func BenchmarkSubmitSmall(b *testing.B) {
	benchmarkSettlePath(b, 1_000)
}

// BenchmarkSubmitLarge：历史 100_000 笔 / 约 9_091 个年度。
func BenchmarkSubmitLarge(b *testing.B) {
	benchmarkSettlePath(b, 100_000)
}

// BenchmarkEngineSubmitLarge：含锁/map 的真实引擎提交路径，历史 100k。
// 通过提交后立即撤销保持理赔号可复用，撤销成本也应与历史规模无关。
func BenchmarkEngineSubmitLarge(b *testing.B) {
	n := 100_000
	e, probe := buildHistory(b, n)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := e.Submit("p", probe); err != nil {
			b.Fatal(err)
		}
		if err := e.Cancel("p", probe.ID); err != nil {
			b.Fatal(err)
		}
	}
}

// TestScaleComparison 以可验证方式对照两档规模下单笔结算耗时不随历史增长。
func TestScaleComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("规模对照在 -short 下跳过")
	}
	measure := func(n int) testing.BenchmarkResult {
		return testing.Benchmark(func(b *testing.B) { benchmarkSettlePath(b, n) })
	}
	small := measure(1_000)
	large := measure(100_000)
	nsSmall := float64(small.T.Nanoseconds()) / float64(small.N)
	nsLarge := float64(large.T.Nanoseconds()) / float64(large.N)
	ratio := nsLarge / nsSmall
	t.Logf("单笔结算: 1k历史=%dns/op, 100k历史=%dns/op, 比值=%.2fx (应≈1.0)",
		int64(nsSmall), int64(nsLarge), ratio)
	// 允许 GC/缓存噪声：100x 历史最多带来 3x 单笔耗时（实际期望≈1）。
	if ratio > 3.0 {
		t.Fatalf("单笔结算耗时随历史增长: 比值 %.2fx", ratio)
	}
}
