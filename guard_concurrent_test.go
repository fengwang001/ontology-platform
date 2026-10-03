package ontology

import (
	"sync"
	"testing"
)

// TestConcurrentSafety 全部操作并发调用：-race 下验证无数据竞争，
// 且账本边界不变量成立（结果等价于某个串行顺序）。
// 各协程占用互不相交的单调时间区间，因此不会产生时钟回退错误。
func TestConcurrentSafety(t *testing.T) {
	g, err := New(Params{N: 4, M: 2, F: 60, SR: 80, S: 50, O: 8, Wt: 4, H: 2, C: 3, Q: 2})
	if err != nil {
		t.Fatal(err)
	}
	const workers, ops = 8, 400
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := int64(w*ops + 1)
			for i := 0; i < ops; i++ {
				ts := base + int64(i)
				switch i % 7 {
				case 0, 1, 2:
					res, aerr := g.Acquire(ts)
					if aerr == nil && (res.Outcome == Granted || res.Outcome == Queued) {
						_ = g.Release(res.ID, i%3 != 0, int64(10+(i%90)), ts+1)
					}
				case 3, 4:
					_ = g.Release(int64(1+(i%12)), i%2 == 0, int64(i%120), ts)
				case 5:
					_, _ = g.Status(int64(1+(i%10)), ts)
				default:
					_, _ = g.Snapshot(ts)
				}
			}
		}(w)
	}
	wg.Wait()

	snap, serr := g.Snapshot(int64(workers*ops + 1))
	if serr != nil {
		t.Fatal(serr)
	}
	if snap.Active > 3 || snap.Queued > 2 {
		t.Fatalf("bounds violated: %+v", snap)
	}
	if snap.State != 0 && snap.Queued != 0 {
		t.Fatalf("queue must be empty when not Closed: %+v", snap)
	}
}
