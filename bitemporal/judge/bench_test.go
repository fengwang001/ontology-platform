package judge

import (
	"testing"

	"ontology/bitemporal"
)

// BenchmarkJudgeBatch 用于人工复核判定开销随记录数的增长趋势。
// 本地验证方法（结果可复核，不依赖特定证明手段）：
//
//	go test -run=^$ -bench=BenchmarkJudgeBatch -benchtime=100x \
//	  -count=1 ./bitemporal/judge
//
// 记录数翻倍时 ns/op 近似翻倍即为线性。版本元数据只在每条记录的常数次步骤中
// 读取 map（O(1) 期望时间），单条记录的轴数恒为 2，故总工作量为 O(n)。
func benchmarkBatch(b *testing.B, n int) {
	eng := newTestEngine()
	rec := bitemporal.Record{ID: "r", Valid: bi(p(1), p(10)), Transaction: bi(p(2), p(8))}
	recs := make([]bitemporal.Record, n)
	for i := range recs {
		recs[i] = rec
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rs := eng.JudgeBatch(recs, "V2", "V1")
		if len(rs) != n {
			b.Fatal("结果数不匹配")
		}
	}
}

func BenchmarkJudgeBatch_100(b *testing.B)   { benchmarkBatch(b, 100) }
func BenchmarkJudgeBatch_1000(b *testing.B)  { benchmarkBatch(b, 1000) }
func BenchmarkJudgeBatch_10000(b *testing.B) { benchmarkBatch(b, 10000) }

// TestLinearScaling 以可编程的方式粗检线性：n 扩大 10 倍时，总耗时不允许超过
// 30 倍（阈值刻意宽松以吸收调度噪声；真正的趋势复核交给 benchmark 输出）。
func TestLinearScaling(t *testing.T) {
	measure := func(n int) int64 {
		eng := newTestEngine()
		rec := bitemporal.Record{ID: "r", Valid: bi(p(1), p(10)), Transaction: bi(p(2), p(8))}
		recs := make([]bitemporal.Record, n)
		for i := range recs {
			recs[i] = rec
		}
		// 重复若干轮取最小，降低噪声。
		var best int64 = 1 << 62
		for round := 0; round < 5; round++ {
			t0 := nanotime()
			_ = eng.JudgeBatch(recs, "V2", "V1")
			elapsed := nanotime() - t0
			if elapsed < best {
				best = elapsed
			}
		}
		return best
	}

	t1 := measure(200)
	t2 := measure(2000)
	ratio := float64(t2) / float64(t1)
	t.Logf("200 条耗时=%dns，2000 条耗时=%dns，比值=%.2f（线性期望约 10）", t1, t2, ratio)
	if ratio > 30 {
		t.Fatalf("耗时比值 %.2f 明显超线性", ratio)
	}
}
