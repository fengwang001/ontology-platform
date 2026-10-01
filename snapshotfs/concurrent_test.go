package snapshotfs

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentStress 并发混合调用：只验证串行化安全（无竞态、无 panic、
// Used<=Cap、编号唯一、状态始终满足不变量）。配合 -race 运行。
func TestConcurrentStress(t *testing.T) {
	l, err := New(10000)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	names := []string{"p", "q", "r"}

	var nextID int64
	var wg sync.WaitGroup
	stop := make(chan struct{})

	worker := func(fn func()) {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				fn()
			}
		}
	}

	// 分配者：编号必须唯一且连续由串行保证；这里仅校验返回 id 不重复。
	seen := sync.Map{}
	wg.Add(1)
	go worker(func() {
		id, e := l.Alloc(7)
		if e != nil {
			return
		}
		if _, loaded := seen.LoadOrStore(id, struct{}{}); loaded {
			t.Errorf("duplicate alloc id %d", id)
		}
		atomic.AddInt64(&nextID, 1)
	})

	// 释放者：只接受非 nil 错误或成功。
	wg.Add(1)
	go worker(func() {
		id := atomic.LoadInt64(&nextID)
		if id == 0 {
			return
		}
		_ = l.Free(id)
	})

	// 快照生命周期工作者。
	for _, nm := range names {
		nm := nm
		wg.Add(3)
		go worker(func() { _ = l.Snapshot(nm) })
		go worker(func() { _ = l.Destroy(nm) })
		go worker(func() {
			if e := l.Hold(nm); e == nil {
				_ = l.Release(nm)
			}
		})
	}

	// 回滚工作者：目标快照可能被持有而拒绝，均合法。
	wg.Add(1)
	go worker(func() {
		for _, nm := range names {
			if e := l.Snapshot(nm); !errors.Is(e, nil) && !errors.Is(e, ErrSnapshotExists) {
				return
			}
			_ = l.Rollback(nm)
		}
	})

	// 查询工作者：持续读取并校验不变量。
	wg.Add(1)
	go worker(func() {
		used := l.Used()
		if used < 0 || used > 10000 {
			t.Errorf("Used out of range: %d", used)
		}
		_ = l.Cur()
		for _, nm := range names {
			_, _ = l.Referenced(nm)
			_, _ = l.Unique(nm)
		}
	})

	// 运行一小段时间后停止。
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	if used := l.Used(); used > 10000 {
		t.Fatalf("final Used %d > cap", used)
	}
}
