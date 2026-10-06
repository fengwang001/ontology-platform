package occupancy

import "testing"

// BenchmarkQueryVsHistory 证明未来时刻点查访问的片段数不随历史许可总数增长。
// 它直接断言 activeAtOrAfter(future)==0（treap 剪枝后的候选规模），
// 再用基准时间展示 Query 不随历史线性变慢。
func BenchmarkQueryVsHistory(b *testing.B) {
	sizes := []int{2000, 8000, 32000}
	for _, n := range sizes {
		s := mustServiceB(b)
		for i := 0; i < n; i++ {
			r := s.Apply(ApplyRequest{
				OpTime: int64(2 * i), Road: "a", Lanes: 1,
				Start: int64(2 * i), End: int64(2*i + 1),
			})
			if !r.Reason.None() {
				b.Fatal(r.Reason)
			}
		}
		idx := s.indices["a"]
		if got := idx.activeAtOrAfter(int64(2*n + 10)); got != 0 {
			b.Fatalf("history=%d but future candidates=%d, query cost must not grow", n, got)
		}
		b.Run(itype(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				q, err := s.Query(QueryRequest{Road: "a", At: int64(2*n + 10)})
				if !err.None() || q.ClosedLanes != 0 {
					b.Fatalf("q=%+v err=%v", q, err)
				}
			}
		})
	}
}

func itype(n int) string {
	switch n {
	case 2000:
		return "history2k"
	case 8000:
		return "history8k"
	case 32000:
		return "history32k"
	}
	return "history"
}

func mustServiceB(tb testing.TB) *Service {
	tb.Helper()
	s, err := NewService(testConfig())
	if !err.None() {
		tb.Fatal(err)
	}
	return s
}
