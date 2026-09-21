package walstore

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentCommitRace 在 -race 下并发提交与读取，随后模拟
// 崩溃恢复，断言所有已返回成功的批次完整可见。
func TestConcurrentCommitRace(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const workers = 8
	const batchesPerWorker = 20
	const keysPerBatch = 3

	batchOf := func(w, i int) map[string]string {
		b := make(map[string]string, keysPerBatch)
		for j := 0; j < keysPerBatch; j++ {
			key := fmt.Sprintf("w%02d-b%02d-k%d", w, i, j)
			b[key] = fmt.Sprintf("v-%d-%d-%d", w, i, j)
		}
		return b
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < batchesPerWorker; i++ {
				b := batchOf(w, i)
				if err := s.Commit(b); err != nil {
					t.Errorf("Commit: %v", err)
					return
				}
				// 与提交并发读取。
				for k := range b {
					s.Get(k)
				}
				s.Len()
			}
		}(w)
	}
	wg.Wait()

	want := workers * batchesPerWorker * keysPerBatch
	if s.Len() != want {
		t.Fatalf("Len = %d, want %d", s.Len(), want)
	}

	// 模拟崩溃：不 Close，直接丢弃并重新 Open。
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if s2.Len() != want {
		t.Fatalf("after recovery Len = %d, want %d", s2.Len(), want)
	}
	for w := 0; w < workers; w++ {
		for i := 0; i < batchesPerWorker; i++ {
			b := batchOf(w, i)
			if vis := batchVisibility(s2, b); vis != keysPerBatch {
				t.Fatalf("batch (%d,%d) visibility = %d, want %d",
					w, i, vis, keysPerBatch)
			}
		}
	}
	s.Close()
}
