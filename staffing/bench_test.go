package staffing

import (
	"fmt"
	"testing"
)

// BenchmarkIssueWithHistory 验证“发放判定开销不随岗位历史通知总数增长”。
//
// 做法：岗位编制固定为 1（无未决），先制造 N 份已终结（拒绝/撤回）的历史通知，
// 再对一次“满编拒绝”发放计时。被计时路径只读取 onboardedCnt/pendingCnt/
// candidatePending/lastBlock/exceptionByKey 这些 O(1) 结构，不遍历历史。
//
// 验证方法：分别在 N=1k/10k/50k 下运行，ns/op 应基本持平（见 go test -bench）。
func BenchmarkIssueWithHistory(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 50_000} {
		b.Run(fmt.Sprintf("history=%d", n), func(b *testing.B) {
			s := New(5, 5)
			mustB(b, s.AddPosition(0, PositionSpec{ID: "P", BandLow: 100, BandHigh: 200, Headcount: 1}))
			mustB(b, s.AddCandidate(0, "hold"))
			// 一份长期未决通知占住唯一编制。
			_, err := s.IssueOffer(0, "hold", "P", 150, 1_000_000)
			mustB(b, err)
			for i := 0; i < n; i++ {
				c := fmt.Sprintf("c%d", i)
				mustB(b, s.AddCandidate(0, c))
			}
			// 制造 n 份历史通知：在另一个岗位发放后立即拒绝（进入历史）。
			mustB(b, s.AddPosition(0, PositionSpec{ID: "H", BandLow: 0, BandHigh: 1_000_000, Headcount: n + 1}))
			for i := 0; i < n; i++ {
				c := fmt.Sprintf("c%d", i)
				id, err := s.IssueOffer(0, c, "H", 100, 10)
				mustB(b, err)
				mustB(b, s.Respond(0, id, false, -1))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// 满编拒绝：在不同候选人上循环以排除“唯一未决”短路的偶然性，
				// 所有候选人冷却都已过（cooldown=5, now=10）。
				c := fmt.Sprintf("c%d", i%n)
				_, _ = s.IssueOffer(10, c, "P", 150, 20)
			}
		})
	}
}

func mustB(b *testing.B, err error) {
	b.Helper()
	if err != nil {
		b.Fatal(err)
	}
}
