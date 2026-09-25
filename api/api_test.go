package api_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

func TestConcurrentApply(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		a, err := api.New(2 * n)
		if err != nil {
			t.Fatal(err)
		}
		var violated atomic.Bool
		done := make(chan struct{})
		var rwg sync.WaitGroup
		rwg.Add(1)
		go func() { // 并发读：Checkpoint 单调不减，Sum 可并发调用
			defer rwg.Done()
			prev := int64(-1)
			for {
				select {
				case <-done:
					return
				default:
					if cp := a.Checkpoint(); cp < prev {
						violated.Store(true)
					} else {
						prev = cp
					}
					_ = a.Sum("k")
				}
			}
		}()
		var wg sync.WaitGroup
		for i := 0; i < n; i++ { // 每个 goroutine 一个互不相同的 offset，顺序打乱
			wg.Add(1)
			go func(off int64) {
				defer wg.Done()
				if err := a.Apply("k", off, 1); err != nil {
					t.Error(err)
				}
			}(int64((i * 31) % n))
		}
		wg.Wait()
		close(done)
		rwg.Wait()
		a.Commit()
		// 与串行执行一致
		serial, _ := api.New(2 * n)
		for i := 0; i < n; i++ {
			if err := serial.Apply("k", int64((i*31)%n), 1); err != nil {
				t.Fatal(err)
			}
		}
		serial.Commit()
		if violated.Load() {
			t.Fatalf("n=%d: checkpoint read decreased", n)
		}
		if a.Checkpoint() != serial.Checkpoint() || a.Sum("k") != serial.Sum("k") ||
			a.Checkpoint() != int64(n-1) || a.Sum("k") != int64(n) {
			t.Fatalf("n=%d: cp=%d sum=%d, want cp=%d sum=%d",
				n, a.Checkpoint(), a.Sum("k"), n-1, n)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	a, err := api.New(16)
	if err != nil {
		t.Fatal(err)
	}
	// 弄脏接收方状态，验证 SelfCheck 在内部实例上运行、不影响外部状态
	_, _, _ = a.Apply("k", 0, 10), a.Apply("k", 2, 20), a.Apply("k", 1, 40)
	a.Commit()
	cp0, sum0 := a.Checkpoint(), a.Sum("k")
	if err := a.SelfCheck(); err != nil {
		t.Fatal(err)
	}
	if a.Checkpoint() != cp0 || a.Sum("k") != sum0 {
		t.Fatal("SelfCheck disturbed receiver state")
	}
	// 并发调用 SelfCheck 也安全
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := a.SelfCheck(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}
