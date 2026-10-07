package meeting

import (
	"fmt"
	"testing"
)

// 本文件的基准测试用于佐证性能承诺：
//   - Raise/Lower/Mute 的队列移出与 QueuePos 不随队列长度线性增长（O(log n)）；
//   - 惰性到期处理的开销只与实际到期轮数有关，与成员总数无关。
// 配合 queue_test.go 中的结点访问计数断言，构成可验证的复杂度证明。

func fillQueue(b *testing.B, r *Room, n int) {
	b.Helper()
	if err := r.Join("host", 0); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		u := fmt.Sprintf("u%d", i)
		if err := r.Join(u, 0); err != nil {
			b.Fatal(err)
		}
		if err := r.Raise(u, 0); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQueuePosFull(b *testing.B) {
	for _, n := range []int{100, 500} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			r, _ := NewRoom(3600, 500)
			fillQueue(b, r, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := r.QueuePos(fmt.Sprintf("u%d", n-1)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRaiseLowerFull(b *testing.B) {
	for _, n := range []int{100, 500} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			r, _ := NewRoom(3600, 500)
			fillQueue(b, r, n)
			victim := fmt.Sprintf("u%d", n-1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := r.Lower(victim, 0); err != nil {
					b.Fatal(err)
				}
				if err := r.Raise(victim, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkHandQueueRemove(b *testing.B) {
	for _, n := range []int{100, 500} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			users := make([]string, n)
			for i := range users {
				users[i] = fmt.Sprintf("u%d", i)
			}
			q := newHandQueue(1)
			fill := func() {
				for _, u := range users {
					q.pushBack(u)
				}
			}
			fill()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if q.len() == 0 {
					b.StopTimer()
					fill()
					b.StartTimer()
				}
				q.remove(users[i%n])
			}
		})
	}
}

// BenchmarkLazyAdvanceNoExpiry 成员很多但没有到期发生时，
// 惰性处理的开销与成员总数无关（O(1)）。
func BenchmarkLazyAdvanceNoExpiry(b *testing.B) {
	for _, members := range []int{10, 500} {
		b.Run(fmt.Sprintf("members=%d", members), func(b *testing.B) {
			r, _ := NewRoom(3600, 500)
			fillQueue(b, r, members-1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r.advance(1) // 无人发言，零到期轮
			}
		})
	}
}

// BenchmarkLazyAdvanceCascade 到期处理的开销正比于实际到期轮数。
func BenchmarkLazyAdvanceCascade(b *testing.B) {
	for _, rounds := range []int{10, 100} {
		b.Run(fmt.Sprintf("rounds=%d", rounds), func(b *testing.B) {
			r, _ := NewRoom(1, 500)
			if err := r.Join("host", 0); err != nil {
				b.Fatal(err)
			}
			users := make([]string, rounds)
			for i := range users {
				users[i] = fmt.Sprintf("u%d", i)
				if err := r.Join(users[i], 0); err != nil {
					b.Fatal(err)
				}
			}
			reset := func() {
				r.speaker = "host"
				r.grantAt = 0
				r.clock = 0
				r.queue = newHandQueue(1)
				for _, u := range users {
					r.queue.pushBack(u)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				reset()
				b.StartTimer()
				r.advance(int64(rounds)) // 恰好 rounds 轮到期
			}
		})
	}
}
