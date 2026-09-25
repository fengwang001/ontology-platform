package qlock

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// 交接必须是 O(1)：无论等待队列多长，一次 Release 只访问自己 + 后继。
func TestReleaseVisitsConstantNodes(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			l := New()
			head, err := l.Acquire()
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for i := 0; i < m; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _ = l.Acquire()
				}()
			}
			for len(l.Snapshot()) != m+1 { // 等 m 个等待者全部链入
				runtime.Gosched()
			}
			if err := l.Release(head); err != nil {
				t.Fatal(err)
			}
			if v := l.visited.Load(); v > 2 {
				t.Fatalf("Release visited %d nodes with queue length %d, want <= 2", v, m)
			}
			for { // 依次释放收尾，让自旋的 goroutine 全部退出
				snap := l.Snapshot()
				if len(snap) == 0 {
					break
				}
				if err := l.Release(snap[0]); err != nil {
					t.Fatal(err)
				}
			}
			wg.Wait()
		})
	}
}
