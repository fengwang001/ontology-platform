package medschedule

import (
	"fmt"
	"testing"
)

// 性能对照：同一固定间隔医嘱运行“很短”与“很长”两档，查询相同宽度小区间。
// 高效实现耗时不应随历史点总数增长；朴素模型必须扫描全部物化点，随历史线性增长。

const perfW int64 = 2
const perfH int64 = 10
const perfWindowWidth int64 = 1000

func buildEffSystem(horizon int64) *System {
	s, err := NewSystem(perfW)
	if err != nil {
		panic(err)
	}
	if err := s.RegisterDrug(0, "D", "C", 1); err != nil {
		panic(err)
	}
	if err := s.CreateOrder(0, Spec{ID: "O", Patient: "P", Drug: "D",
		Kind: "interval", FirstTime: 0, H: perfH}); err != nil {
		panic(err)
	}
	return s
}

func buildNaiveLong(horizon int64) *NaiveModel {
	m := NewNaiveModel(perfW)
	if err := m.RegisterDrug(0, "D", "C", 1); err != nil {
		panic(err)
	}
	if err := m.CreateOrder(0, Spec{ID: "O", Patient: "P", Drug: "D",
		Kind: "interval", FirstTime: 0, H: perfH}); err != nil {
		panic(err)
	}
	m.materializeAll(horizon)
	return m
}

func runEffQuery(b *testing.B, horizon int64) {
	s := buildEffSystem(horizon)
	lo := horizon - perfWindowWidth
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.QueryPatient(horizon, "P", lo, horizon); err != nil {
			b.Fatal(err)
		}
	}
}

func runNaiveQuery(b *testing.B, horizon int64) {
	m := buildNaiveLong(horizon)
	lo := horizon - perfWindowWidth
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.NaiveQuery(horizon, "P", lo, horizon)
	}
}

func BenchmarkQueryEfficientShort(b *testing.B) { runEffQuery(b, 10_000) }
func BenchmarkQueryEfficientLong(b *testing.B)  { runEffQuery(b, 100_000_000) }
func BenchmarkQueryNaiveShort(b *testing.B)     { runNaiveQuery(b, 10_000) }
func BenchmarkQueryNaiveLong(b *testing.B)      { runNaiveQuery(b, 100_000_000) }

// TestCostIndependentOfHistory 用实际计时断言高效查询在两档历史长度下耗时接近，
// 且朴素模型长历史明显更慢，可验证开销不随历史点总数增长。
func TestCostIndependentOfHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping perf in short mode")
	}
	measure := func(horizon int64, eff bool) float64 {
		var r testing.BenchmarkResult
		if eff {
			r = testing.Benchmark(func(b *testing.B) { runEffQuery(b, horizon) })
		} else {
			r = testing.Benchmark(func(b *testing.B) { runNaiveQuery(b, horizon) })
		}
		return float64(r.T.Nanoseconds()) / float64(r.N)
	}

	const short, long = int64(10_000), int64(100_000_000)
	shortEff, longEff := measure(short, true), measure(long, true)
	shortNv, longNv := measure(short, false), measure(long, false)
	t.Logf("efficient: short(%d pts)=%.0fns/op long(%d pts)=%.0fns/op ratio=%.2fx",
		short/perfH, shortEff, long/perfH, longEff, longEff/shortEff)
	t.Logf("naive:     short=%.0fns/op long=%.0fns/op ratio=%.1fx",
		shortNv, longNv, longNv/shortNv)
	fmt.Println("perf comparison printed in test log (-v)")
	if ratio := longEff / shortEff; ratio > 4.0 {
		t.Fatalf("efficient query scaled with history: ratio=%.2fx", ratio)
	}
	if longNv < shortNv*50 {
		t.Fatalf("naive query unexpectedly flat: short=%.0f long=%.0f", shortNv, longNv)
	}
}
