package floorcontrol

import (
	"fmt"
	"testing"
)

// BenchmarkQueueOps 证明队列的插入/按下标删除/名次查询不随队列长度线性退化：
// 比较 n=2^10、2^14、2^18 三档规模下单次操作耗时，期望均为 O(log n) 级别。
func BenchmarkQueueOps(b *testing.B) {
	for _, n := range []int{1 << 10, 1 << 14, 1 << 18} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			users := make([]string, n)
			for i := range users {
				users[i] = fmt.Sprintf("u%d", i)
			}
			b.Run("RaiseRemove", func(b *testing.B) {
				q := newHandQueue()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					u := users[i%n]
					q.push(u, int64(i+1))
					q.remove(u)
				}
			})
			b.Run("QueuePos", func(b *testing.B) {
				q := newHandQueue()
				for i := range n {
					q.push(users[i], int64(i+1))
				}
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_, _ = q.position(users[i%n])
				}
			})
		})
	}
}
