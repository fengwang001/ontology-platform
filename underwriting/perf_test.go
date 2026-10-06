package underwriting

import (
	"fmt"
	"testing"
)

// 性能对照：构造两档规模的规则库，额外规则与投保单的年龄、职业都不相关
// （职业类别 2、年龄区间 [60,70]，投保单为职业 1、30 岁）。
// 若裁定开销与无关规则数量无关，两档的单次裁定耗时应基本相等。

func buildPerfEngine(tb testing.TB, irrelevant int) *Engine {
	tb.Helper()
	e := NewEngine(testConfig())
	for i := 0; i < irrelevant; i++ {
		r := loadingRule(fmt.Sprintf("noise-%d", i), 5,
			Condition{AgeLo: intPtr(60), AgeHi: intPtr(70), Occupations: map[int]bool{2: true}})
		if err := e.AddRule(r); err != nil {
			tb.Fatal(err)
		}
	}
	// 少量与投保单相关的规则，保证裁定有实际工作。
	_ = e.AddRule(loadingRule("rel-1", 10, Condition{AgeLo: intPtr(20), AgeHi: intPtr(40)}))
	_ = e.AddRule(Rule{ID: "rel-2", Cond: Condition{Occupations: map[int]bool{1: true, 3: true}},
		Act: Action{Kind: ActionExclusion, ExclusionCode: "EX1"}, Start: 0, End: 1 << 40})
	_ = e.AddRule(Rule{ID: "rel-3", Cond: Condition{HealthCodes: map[string]bool{"H1": true}},
		Act: Action{Kind: ActionLoading, Percent: 5}, Start: 0, End: 1 << 40})
	return e
}

func benchFirstDecision(b *testing.B, irrelevant int) {
	e := buildPerfEngine(b, irrelevant)
	// 预热：触发索引重建，使其不计入计时。
	if _, err := e.Register(baseApp("warmup")); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		app := baseApp(fmt.Sprintf("app-%d", i))
		if _, err := e.Register(app); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFirstDecisionIrrelevant1k(b *testing.B)   { benchFirstDecision(b, 1_000) }
func BenchmarkFirstDecisionIrrelevant100k(b *testing.B) { benchFirstDecision(b, 100_000) }

// 以可验证的方式证明：单次裁定开销不随无关规则数量增长。
// 对照 1 千条与 10 万条无关规则两档规模，耗时比值应远小于规模比值（100 倍）。
func TestDecisionCostIndependentOfIrrelevantRules(t *testing.T) {
	small := testing.Benchmark(func(b *testing.B) { benchFirstDecision(b, 1_000) })
	large := testing.Benchmark(func(b *testing.B) { benchFirstDecision(b, 100_000) })
	ratio := float64(large.NsPerOp()) / float64(small.NsPerOp())
	t.Logf("无关规则 1k: %d ns/op; 无关规则 100k: %d ns/op; 比值=%.2f（规模比=100）",
		small.NsPerOp(), large.NsPerOp(), ratio)
	if ratio > 5 {
		t.Fatalf("裁定开销随无关规则数量显著增长: 比值 %.2f 超过阈值 5", ratio)
	}
}
