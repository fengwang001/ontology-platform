package srcmap

import (
	"fmt"
	"testing"
)

var (
	sinkMap  *Mapping
	sinkInts int
)

// BenchmarkLookupSublinear：单行 100k 段时，点查询应只付出二分代价。
func BenchmarkLookupSublinear(b *testing.B) {
	const n = 100_000
	segs := make([]Segment, n)
	for i := range segs {
		segs[i] = mapped(i, 0, 0, i)
	}
	m := mustNew(b, 1, []Line{{GeneratedLine: 0, Segments: segs}})
	b.ResetTimer()
	var sink LookupResult
	for i := 0; i < b.N; i++ {
		r, err := m.Lookup(0, n-1)
		if err != nil {
			b.Fatal(err)
		}
		sink = r
	}
	_ = sink
	// 判定依据：100k 段下单次查询约 O(log n)≈17 次比较，
	// 预期 ns/op 与段总数无关（见 go test -bench 输出）。
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N), "ns/op-observed")
}

// BenchmarkComposeLinear：M1、M2 各 N 段且合成不跨边界时，
// 工作量约为 O(N)，而非 O(N^2)。
func BenchmarkComposeLinear(b *testing.B) {
	for _, n := range []int{10_000, 40_000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			m1segs := make([]Segment, n)
			for i := range m1segs {
				m1segs[i] = mapped(i, 0, i%2, i) // 相邻段原始行交替 → 不可合并
			}
			m1segs = append(m1segs, unmapped(n))
			m1 := mustNew(b, 1, []Line{{GeneratedLine: 0, Segments: m1segs}})
			// M2 每行只有一个段，映射到同一中间行，顺序推进。
			m2segs := []Segment{mapped(0, 0, 0, 0), unmapped(n)}
			m2 := mustNew(b, 1, []Line{{GeneratedLine: 0, Segments: m2segs}})
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r, err := Compose(m2, m1)
				if err != nil {
					b.Fatal(err)
				}
				sinkMap = r
				sinkInts += len(r.lines) + len(r.lines[0].Segments)
			}
		})
	}
	// 判定依据：N 增大 4 倍，时间应约增大 4 倍（线性），而非 16 倍（乘积）。
}
