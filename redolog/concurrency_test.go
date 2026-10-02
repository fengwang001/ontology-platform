package redolog

import (
	"sync"
	"testing"
)

// 所有操作与查询可并发调用（配合 -race 验证），结果等价于某个串行顺序。
func TestConcurrentAccess(t *testing.T) {
	r := mustNew(t, 4, 50)
	mustLoad(t, r, 1, 0, 0, 0)
	var wg sync.WaitGroup
	// 单生产者按 LSN 升序喂日志。
	wg.Add(1)
	go func() {
		defer wg.Done()
		lsn := int64(0)
		mtr := int64(0)
		for i := 0; i < 200; i++ {
			mtr++
			lsn++
			if err := r.Feed(page(lsn, mtr, 1, int64(i%8), 1)); err != nil {
				t.Errorf("Feed Page 失败: %v", err)
				return
			}
			lsn++
			if err := r.Feed(end(lsn, mtr)); err != nil {
				t.Errorf("Feed End 失败: %v", err)
				return
			}
		}
	}()
	// 多协程并发查询。
	for k := 0; k < 4; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_ = r.Pending()
				_ = r.Batches()
				_ = r.Stats()
				_ = r.ApplyLog()
				_, _, _ = r.PageState(1, int64(i%8))
			}
		}()
	}
	wg.Wait()
	if err := r.Finish(); err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	st := r.Stats()
	if st.AcceptedPages != 200 || st.Applied != 200 {
		t.Fatalf("统计错误: %+v", st)
	}
	v, l, ok := r.PageState(1, 0)
	if !ok || v != 25 || l == 0 {
		t.Fatalf("页 (1,0) = (%d,%d)，期望 (25,非0)", v, l)
	}
}

// 构造参数边界：W=64、M=10^6 合法；M 很大时不到 M 不触发批次，Finish 统一结算。
func TestLargeBatchLimit(t *testing.T) {
	r := mustNew(t, 64, 1_000_000)
	mustLoad(t, r, 1, 0, 0, 0)
	mustFeed(t, r, page(1, 1, 1, 0, 5))
	mustFeed(t, r, end(2, 1))
	if got := r.Batches(); got != 0 {
		t.Fatalf("pending=1 远小于 M=10^6 不应批次，batches = %d", got)
	}
	if err := r.Finish(); err != nil {
		t.Fatalf("Finish 失败: %v", err)
	}
	if got := r.Batches(); got != 1 {
		t.Fatalf("Finish 应结算一个批次，batches = %d", got)
	}
	checkPage(t, r, 1, 0, 5, 1)
}
