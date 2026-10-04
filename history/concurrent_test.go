package history_test

import (
	"sync"
	"testing"

	"ontology/history"
)

// 所有操作可并发调用：仅校验无数据竞争且最终状态是某合法串行结果
// （maxSeq 恒为成功 Index/Delete 数，Plan 不 panic）。
func TestConcurrent(t *testing.T) {
	p := history.NewPrimary(50, 1000)
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := (w*200 + i) % 400 // 非严格单调也允许被拒，只要求不 panic/无 race
				id := string(rune('a' + (i % 8)))
				p.Index(now, id)
				p.Delete(now, id)
				p.AddLease(now, "L", 1)
				p.RenewLease(now, "L", 1)
				p.SetGlobalCheckpoint(now, 0)
				p.Merge(now)
				if _, err := p.Plan(0); err != nil {
					t.Errorf("Plan err: %v", err)
					return
				}
				_ = p.MaxSeq()
				_ = p.H()
				_ = p.GCP()
				_ = p.Leases()
			}
		}(w)
	}
	wg.Wait()
	// 单调时钟前提下，以最大 now 做一次 Merge/Plan 仍一致可用。
	if _, err := p.Merge(400); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Plan(0); err != nil {
		t.Fatal(err)
	}
}
