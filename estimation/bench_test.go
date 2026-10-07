package estimation

import (
	"fmt"
	"testing"
)

// 本文件的基准用于验证性能约束：
//   - Vote/Unvote 与自动揭示条件判断的开销不随参与者总数线性增长；
//   - 揭示（evaluate）的开销只与牌组大小有关，与参与者总数无关。
//
// 运行：go test -bench=. -benchmem ./estimation/
// 预期：同一基准在 participants=1e3/1e4/1e5 下的 ns/op 基本持平。

func benchSession(b *testing.B, participants int, auto bool) *Session {
	s, err := NewSession("host", []int{1, 2, 3, 5, 8, 13, 21, 34}, 5, MaxRoundTTL, auto)
	if err != nil {
		b.Fatalf("NewSession: %v", err)
	}
	for i := 0; i < participants; i++ {
		if _, err := s.Join(fmt.Sprintf("u%d", i), RoleVoter, 0); err != nil {
			b.Fatalf("Join: %v", err)
		}
	}
	if _, err := s.Start("host", 0); err != nil {
		b.Fatalf("Start: %v", err)
	}
	return s
}

// BenchmarkVote 在自动揭示开启下测量 Vote（内含自动揭示条件判断）。
// 保留一名投票者永不投票，避免真的触发揭示。
func BenchmarkVote(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("participants=%d", n), func(b *testing.B) {
			s := benchSession(b, n, true)
			cards := []Card{1, 2, 3, 5}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Vote("u0", cards[i%len(cards)], 0); err != nil {
					b.Fatalf("Vote: %v", err)
				}
			}
		})
	}
}

// BenchmarkUnvote 测量 Unvote。
func BenchmarkUnvote(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("participants=%d", n), func(b *testing.B) {
			s := benchSession(b, n, false)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Unvote("u0", 0); err != nil {
					b.Fatalf("Unvote: %v", err)
				}
			}
		})
	}
}

// BenchmarkRevealEvaluate 直接测量揭示判定本身（Deck.evaluate），
// 参与者全部投同一数值牌，分布计数器已增量维护完毕。
func BenchmarkRevealEvaluate(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("participants=%d", n), func(b *testing.B) {
			s := benchSession(b, n, false)
			for i := 0; i < n; i++ {
				if _, err := s.Vote(fmt.Sprintf("u%d", i), Card(3), 0); err != nil {
					b.Fatalf("Vote: %v", err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out := s.deck.evaluate(&s.box, 1, 5, 0, TriggerManual)
				if out.Kind != ResultConsensus {
					b.Fatalf("unexpected: %+v", out)
				}
			}
		})
	}
}

// BenchmarkRevealEvaluateDiverged 测量最坏情形：票分散在牌组各处，
// 需要完整扫描牌组求最小最大位置与下中位。
func BenchmarkRevealEvaluateDiverged(b *testing.B) {
	deck := []int{1, 2, 3, 5, 8, 13, 21, 34}
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("participants=%d", n), func(b *testing.B) {
			s := benchSession(b, n, false)
			for i := 0; i < n; i++ {
				if _, err := s.Vote(fmt.Sprintf("u%d", i), Card(deck[i%len(deck)]), 0); err != nil {
					b.Fatalf("Vote: %v", err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out := s.deck.evaluate(&s.box, 5, 5, 0, TriggerManual)
				if out.Kind != ResultForced {
					b.Fatalf("unexpected: %+v", out)
				}
			}
		})
	}
}
