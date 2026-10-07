package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentSequentialNumbers 并发动作执行下：序号唯一、连续、无空洞，
// 最终状态等价于某个全局串行顺序。
func TestConcurrentSequentialNumbers(t *testing.T) {
	store, exec := newSeeded(t, "X")
	const workers = 16
	const perWorker = 200

	var wg sync.WaitGroup
	errs := make(chan error, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				_, err := exec.ExecuteAction(
					fmt.Sprintf("w%d-%d", id, i),
					map[string]string{"X": fmt.Sprintf("w%d-i%d", id, i)},
					false,
				)
				if err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发动作出错: %v", err)
	}

	total := workers * perWorker
	if store.Len() != total {
		t.Fatalf("记录数=%d 期望 %d", store.Len(), total)
	}
	seen := map[int]bool{}
	records := store.All()
	values := map[string]bool{}
	for _, r := range records {
		if seen[r.Seq] {
			t.Fatalf("重复序号 %d", r.Seq)
		}
		seen[r.Seq] = true
		values[r.Changes[0].After] = true
	}
	for i := 1; i <= total; i++ {
		if !seen[i] {
			t.Fatalf("序号空洞: 缺少 %d", i)
		}
	}
	// 串行等价性：最终值必然是某个被提交动作写入的值（且唯一）。
	final := NewReplayer(store).StateAt(total)
	if !values[final["X"]] {
		t.Fatalf("最终状态不对应任何串行提交: %s", final["X"])
	}
	if err := store.verifyHashChain(); err != nil {
		t.Fatalf("并发后哈希链损坏: %v", err)
	}
	t.Logf("并发完成 total=%d final=%s 哈希链OK", total, final["X"])
	fmt.Printf("依据: %d 个并发动作 -> 序号1..%d 唯一连续无空洞，最终值=%s 且哈希链通过\n",
		total, total, final["X"])
}
