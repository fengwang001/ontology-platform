package planningpoker

import (
	"fmt"
	"strconv"
	"testing"
)

// BenchmarkVoteO1 验证 Vote/Unvote 与自动揭示条件判定不随参与者规模增长。
// 1k 与 32k 名“沉默”在室投票者并存时，对同一探测者反复改投/撤回，
// 单次 ns/op 应处于同一量级（差异主要来自缓存，而非线性扫描）。
func BenchmarkVoteO1(b *testing.B) {
	for _, n := range []int{1_000, 32_000} {
		b.Run("N"+strconv.Itoa(n), func(b *testing.B) {
			s, base := populatedSession(b, n, false)
			u := "probe"
			mustJoinB(b, s, u, RoleVoter, base)
			mustStartB(b, s, base+1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				now := base + 2 + int64(i)
				if i%2 == 0 {
					if _, err := s.Vote(u, numCard(3), now); err != nil {
						b.Fatal(err)
					}
				} else {
					if _, err := s.Unvote(u, now); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

// BenchmarkRevealVsMembers 验证揭示统计开销只随牌组大小变化、
// 不随参与者数变化：同一 8 张牌组在 1k 与 32k 名全投投票者下揭示，
// 用于对比 ns/op。揭示统计/分布只遍历牌组（成员快照遍历另计）。
func BenchmarkRevealVsMembers(b *testing.B) {
	for _, n := range []int{1_000, 32_000} {
		b.Run("N"+strconv.Itoa(n), func(b *testing.B) {
			for k := 0; k < b.N; k++ {
				b.StopTimer()
				s, base := populatedSession(b, n, false)
				mustStartB(b, s, base)
				for i := 0; i < n; i++ {
					if _, err := s.Vote(benchUser(i), numCard(3), base+1); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
				r, err := s.Reveal("host", base+2)
				if err != nil || r.NumericVotes != n {
					b.Fatalf("reveal: %v %+v", err, r)
				}
			}
		})
	}
}

// populatedSession 创建带 n 名投票者（尚未开始议题）的会话，
// 返回可安全使用的下一个时间戳 base。
func populatedSession(b testing.TB, n int, auto bool) (*Session, int64) {
	b.Helper()
	s, err := NewSession("host", Config{
		Cards: []int{1, 2, 3, 5, 8, 13, 21, 34}, RoundLimit: 1,
		RoundSeconds: 86400, AutoReveal: auto,
	}, 0)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		mustJoinB(b, s, benchUser(i), RoleVoter, int64(1+i))
	}
	return s, int64(n + 1)
}

func mustJoinB(b testing.TB, s *Session, u string, role Role, now int64) {
	b.Helper()
	if _, err := s.Join(u, role, now); err != nil {
		b.Fatal(err)
	}
}

func mustStartB(b testing.TB, s *Session, now int64) {
	b.Helper()
	if err := s.Start("host", now); err != nil {
		b.Fatal(err)
	}
}

// benchUser 用定长零填充，避免 m1/m10 这类字符串拼接碰撞。
func benchUser(i int) string { return fmt.Sprintf("m%06d", i) }

// BenchmarkRevealDeckSizes 固定参与者数（1000），仅改变牌组大小，
// 展示揭示统计随牌组线性、与参与者无关。
func BenchmarkRevealDeckSizes(b *testing.B) {
	for _, d := range []int{2, 8, 20} {
		b.Run("D"+strconv.Itoa(d), func(b *testing.B) {
			cards := make([]int, d)
			for i := range cards {
				cards[i] = (i + 1) * 3
			}
			const n = 1000
			for k := 0; k < b.N; k++ {
				b.StopTimer()
				s, err := NewSession("host", Config{
					Cards: cards, RoundLimit: 1, RoundSeconds: 86400, AutoReveal: false,
				}, 0)
				if err != nil {
					b.Fatal(err)
				}
				for i := 0; i < n; i++ {
					mustJoinB(b, s, benchUser(i), RoleVoter, int64(1+i))
				}
				mustStartB(b, s, int64(n+1))
				for i := 0; i < n; i++ {
					card := numCard(cards[i%d])
					if _, err := s.Vote(benchUser(i), card, int64(n+2)); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
				if _, err := s.Reveal("host", int64(n+3)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
