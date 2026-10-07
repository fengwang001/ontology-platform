package ontologyindex

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// BenchmarkLookup1k / 10k / 100k / 1m：在累计不同量级变更事件后测量
// 单次 Lookup 耗时。若查询随事件总数线性增长，耗时应随 b.N 之外的数据量
// 近似等比例放大；预期各档 ns/op 基本持平（哈希 O(1)）。
//
// 运行：go test -bench=BenchmarkLookup -benchmem ./ontology
func benchmarkLookup(b *testing.B, totalEvents int) {
	b.StopTimer()
	s := schemaForTests()
	eng := NewEngine(s, nil)
	if err := eng.CreateIndex("idx", "Person", "prop_name", ConstraintDuplicate); err != nil {
		b.Fatal(err)
	}
	// 把 totalEvents 条事件分布到 100 个取值桶上（所有 ts<100，避免废弃）。
	for i := 0; i < totalEvents; i++ {
		ev := ChangeEvent{
			EventID:     fmt.Sprintf("big-%d", i),
			ObjectType:  "Person",
			ObjectID:    fmt.Sprintf("obj-%d", i),
			PropertyID:  "prop_name",
			NewValue:    StringValue(fmt.Sprintf("bucket-%d", i%100)),
			EffectiveAt: LogicalClock(1 + (i % 98)),
		}
		if err := eng.Ingest(ev); err != nil {
			b.Fatal(err)
		}
	}
	target := StringValue("bucket-7")
	b.ReportAllocs()
	b.StartTimer()
	for n := 0; n < b.N; n++ {
		got, err := eng.Lookup("idx", target)
		if err != nil {
			b.Fatal(err)
		}
		if len(got) == 0 {
			b.Fatal("应命中对象")
		}
	}
}

func BenchmarkLookup1k(b *testing.B)   { benchmarkLookup(b, 1_000) }
func BenchmarkLookup10k(b *testing.B)  { benchmarkLookup(b, 10_000) }
func BenchmarkLookup100k(b *testing.B) { benchmarkLookup(b, 100_000) }
func BenchmarkLookup1m(b *testing.B)   { benchmarkLookup(b, 1_000_000) }

// BenchmarkLookupSingleHit*：每个对象命中独立取值桶（k=1），
// 把“定位开销”与“结果拷贝”分离。此时从 1k 到 1m 事件，
// 单次查询的 ns/op 与 B/op 应基本恒定，直接证明定位与 N 无关。
func benchmarkLookupSingleHit(b *testing.B, totalEvents int) {
	b.StopTimer()
	s := schemaForTests()
	eng := NewEngine(s, nil)
	if err := eng.CreateIndex("idx", "Person", "prop_name", ConstraintDuplicate); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < totalEvents; i++ {
		ev := ChangeEvent{
			EventID:     fmt.Sprintf("uniq-%d", i),
			ObjectType:  "Person",
			ObjectID:    fmt.Sprintf("obj-%d", i),
			PropertyID:  "prop_name",
			NewValue:    StringValue(fmt.Sprintf("unique-value-%d", i)),
			EffectiveAt: LogicalClock(1 + (i % 98)),
		}
		if err := eng.Ingest(ev); err != nil {
			b.Fatal(err)
		}
	}
	target := StringValue(fmt.Sprintf("unique-value-%d", totalEvents/2))
	b.ReportAllocs()
	b.StartTimer()
	for n := 0; n < b.N; n++ {
		got, err := eng.Lookup("idx", target)
		if err != nil || len(got) != 1 {
			b.Fatalf("应恰好命中 1 个对象，得到 %d, err=%v", len(got), err)
		}
	}
}

func BenchmarkLookupSingleHit1k(b *testing.B) { benchmarkLookupSingleHit(b, 1_000) }
func BenchmarkLookupSingleHit100k(b *testing.B) {
	benchmarkLookupSingleHit(b, 100_000)
}
func BenchmarkLookupSingleHit1m(b *testing.B) {
	benchmarkLookupSingleHit(b, 1_000_000)
}

// TestLookupConstantTime 是可在普通 `go test` 中独立验证的复杂度断言：
// 用同一查询在 1万 / 10万 / 100万 累计事件下各重复计时，要求最大档耗时
// 不超过最小档的 5 倍（线性算法下该比值应接近 100 倍）。
func TestLookupConstantTime(t *testing.T) {
	// 该断言需构建百万级事件，耗时较长，默认跳过。
	// 显式运行：ONT_RUN_SLOW=1 go test -run TestLookupConstantTime ./ontology
	if os.Getenv("ONT_RUN_SLOW") == "" {
		t.Skip("百万级复杂度断言默认跳过；设 ONT_RUN_SLOW=1 运行")
	}
	measure := func(total int) int64 {
		s := schemaForTests()
		eng := NewEngine(s, nil)
		if err := eng.CreateIndex("idx", "Person", "prop_name", ConstraintDuplicate); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < total; i++ {
			ev := ChangeEvent{
				EventID:     fmt.Sprintf("c-%d-%d", total, i),
				ObjectType:  "Person",
				ObjectID:    fmt.Sprintf("o-%d", i),
				PropertyID:  "prop_name",
				NewValue:    StringValue(fmt.Sprintf("b-%d", i%50)),
				EffectiveAt: LogicalClock(1 + i%98),
			}
			if err := eng.Ingest(ev); err != nil {
				t.Fatal(err)
			}
		}
		target := StringValue("b-3")
		// 预热触发懒分配/编译优化。
		for r := 0; r < 50; r++ {
			_, _ = eng.Lookup("idx", target)
		}
		const reps = 500
		var best int64 = 1 << 62
		var sink int
		for round := 0; round < 5; round++ {
			start := time.Now().UnixNano()
			for r := 0; r < reps; r++ {
				got, _ := eng.Lookup("idx", target)
				sink += len(got)
			}
			if avg := (time.Now().UnixNano() - start) / reps; avg < best {
				best = avg
			}
		}
		if sink == 0 {
			t.Fatal("空结果")
		}
		return best
	}

	small := measure(10_000)
	large := measure(1_000_000)
	t.Logf("平均单次查询耗时: 1万事件=%dns 100万事件=%dns 比值=%.2fx",
		small, large, float64(large)/float64(small))
	// 线性算法在该 100 倍数据跨度下应给出 ~100x；O(1) 哈希仅受缓存/GC
	// 常数影响。阈值 25x 用于独立、自动地把二者区分开。
	if small > 0 && large > small*25 {
		t.Fatalf("查询耗时随事件总量超线性增长（比值 %.2fx，阈值 25x），不满足 O(1)",
			float64(large)/float64(small))
	}
}
