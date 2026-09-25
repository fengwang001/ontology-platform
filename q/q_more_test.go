package q

import (
	"sync"
	"sync/atomic"
	"testing"
)

// 复杂度：任意 m 下单次 Dequeue 访问节点数恒为 1（同包测试读非导出字段，
// 该字段不出现在任何公开接口里）。
func TestDequeueVisitsOneNode(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		qu, _ := New(m)
		for i := 0; i < m; i++ {
			_ = qu.Enqueue(i)
		}
		v, ok := qu.Dequeue()
		if !ok || v != 0 {
			t.Fatalf("m=%d: deq = (%d,%v)", m, v, ok)
		}
		if got := qu.lastVisit.Load(); got != 1 {
			t.Fatalf("m=%d: dequeue visited %d nodes, want 1", m, got)
		}
	}
}

// 并发：N 生产者各入队一个唯一值，N 消费者并发出队，
// 取出的 N 个值必须恰好是那 N 个唯一值，各出现一次。通道/原子收尾，不用 sleep。
func TestConcurrentMPMC(t *testing.T) {
	for _, n := range []int{64, 1024} {
		qu, _ := New(n)
		ch := make(chan int, n)
		var got atomic.Int64
		var wg sync.WaitGroup
		for p := 0; p < n; p++ {
			wg.Add(1)
			go func(v int) { defer wg.Done(); _ = qu.Enqueue(v) }(p)
		}
		for c := 0; c < n; c++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for got.Load() < int64(n) {
					if v, ok := qu.Dequeue(); ok {
						got.Add(1)
						ch <- v
					}
				}
			}()
		}
		wg.Wait()
		close(ch)
		seen := make(map[int]int)
		for v := range ch {
			seen[v]++
			if seen[v] != 1 || v < 0 || v >= n {
				t.Fatalf("n=%d: value %d count %d", n, v, seen[v])
			}
		}
		if len(seen) != n {
			t.Fatalf("n=%d: %d distinct values, want %d", n, len(seen), n)
		}
	}
}
