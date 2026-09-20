package ontology

import (
	"sync"
	"testing"
)

// startBarrier 让所有 goroutine 尽量同时出发，不用 time.Sleep。
func startBarrier() (wait func(), go_ func()) {
	ready := make(chan struct{})
	return func() { <-ready }, func() { close(ready) }
}

// 并发更新：所有成功更新的版本号必须互不相同且严格落在预期区间内。
func TestConcurrentUpdatesUniqueVersions(t *testing.T) {
	m := newTestManager(t)
	const workers = 32
	const perWorker = 25
	wait, go_ := startBarrier()
	versions := make([][]uint64, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			wait()
			for i := 0; i < perWorker; i++ {
				v, err := m.Update(map[string]any{"retries": int64(i % 11)})
				if err != nil {
					t.Errorf("Update: %v", err)
					return
				}
				versions[w] = append(versions[w], v)
			}
		}(w)
	}
	go_()
	wg.Wait()
	seen := make(map[uint64]bool, workers*perWorker)
	for _, vs := range versions {
		for _, v := range vs {
			if seen[v] {
				t.Fatalf("duplicate version number %d", v)
			}
			seen[v] = true
		}
	}
	if len(seen) != workers*perWorker {
		t.Fatalf("got %d distinct versions, want %d", len(seen), workers*perWorker)
	}
	if got := m.Current(); got != uint64(1+workers*perWorker) {
		t.Fatalf("current = %d, want %d", got, 1+workers*perWorker)
	}
}

// 并发 Acquire/Release：结束后未归还数必须精确为零，引用计数不得错乱。
func TestConcurrentAcquireRelease(t *testing.T) {
	m := newTestManager(t)
	const workers = 32
	const perWorker = 50
	wait, go_ := startBarrier()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			wait()
			for i := 0; i < perWorker; i++ {
				s := m.Acquire()
				if _, err := s.Get("host"); err != nil {
					t.Errorf("Get on live snapshot: %v", err)
				}
				s.Release()
				if i%3 == 0 {
					s.Release() // 重复归还必须无害
				}
			}
		}(w)
	}
	go_()
	wg.Wait()
	rep := m.Leaks()
	if rep.Outstanding != 0 {
		t.Fatalf("outstanding = %d, want 0", rep.Outstanding)
	}
	if len(rep.ByVersion) != 0 {
		t.Fatalf("by-version = %v, want empty", rep.ByVersion)
	}
}

// 更新、获取、归还、回收并发交错：不变量始终成立——
// 当前版本永不回收，结束后计数归零，版本号不复用。
func TestConcurrentMixedOperations(t *testing.T) {
	m := newTestManager(t)
	const workers = 16
	const perWorker = 40
	wait, go_ := startBarrier()
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			wait()
			for i := 0; i < perWorker; i++ {
				switch i % 4 {
				case 0:
					if _, err := m.Update(map[string]any{
						"retries": int64((w + i) % 11),
					}); err != nil {
						t.Errorf("Update: %v", err)
						return
					}
				case 1:
					s := m.Acquire()
					if _, err := s.Get("port"); err != nil {
						t.Errorf("Get: %v", err)
					}
					s.Release()
				case 2:
					for _, num := range m.Collect() {
						if num == m.Current() {
							t.Errorf("Collect reclaimed current version %d", num)
						}
					}
				default:
					_ = m.Leaks()
				}
			}
		}(w)
	}
	go_()
	wg.Wait()
	if rep := m.Leaks(); rep.Outstanding != 0 {
		t.Fatalf("outstanding = %d, want 0", rep.Outstanding)
	}
	m.Collect()
	if _, err := m.Update(map[string]any{"host": "final"}); err != nil {
		t.Fatalf("final Update: %v", err)
	}
	final := m.Current()
	if got, err := m.GetAt(final, "host"); err != nil || got != "final" {
		t.Fatalf("GetAt(%d) = %v, %v", final, got, err)
	}
}
