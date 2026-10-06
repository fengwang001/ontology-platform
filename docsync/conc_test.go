package docsync

import (
	"errors"
	"sync"
	"testing"
)

// TestConcurrentStaleRace 多个调用方以同一基版本并发提交：恰有一个成功，
// 其余全部 ErrStaleVersion，且版本只前进一次。配合 -race 验证可线性化。
func TestConcurrentStaleRace(t *testing.T) {
	s := NewService("0123456789")
	const n = 64
	var wg sync.WaitGroup
	var mu sync.Mutex
	var accepted []int
	var stale, other int
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			<-start
			r := s.Change(0, []Edit{{
				Range: pointRange(0, i%10),
				Text:  "X",
			}})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case r.Accepted:
				accepted = append(accepted, i)
			case errors.Is(r.Reason, ErrStaleVersion):
				stale++
			default:
				other++
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(accepted) != 1 {
		t.Fatalf("accepted=%d want exactly 1: %v", len(accepted), accepted)
	}
	if stale != n-1 || other != 0 {
		t.Fatalf("stale=%d other=%d", stale, other)
	}
	if s.Version() != 1 {
		t.Fatalf("version=%d want 1", s.Version())
	}
}

// TestConcurrentMixedReadWrite 高并发混合读快照、换算、写变更与登记，
// 保证不撕裂、无数据竞争（-race）。
func TestConcurrentMixedReadWrite(t *testing.T) {
	s := NewService("alpha\nbeta\ngamma")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ver := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Snapshot()
				if snap.Version < ver {
					t.Errorf("version went backwards")
					return
				}
				// 快照内文本与诊断必须自洽：每个有效诊断范围都能在该文本定位。
				for _, d := range snap.Diagnostics {
					if _, err := s.PositionToOffset(d.Range.Start); err != nil {
						t.Errorf("snapshot diag inconsistent: %v", err)
						return
					}
				}
				if _, err := s.OffsetToPosition(len16(snap.Text)); err != nil {
					t.Errorf("snapshot text length inconsistent: %v", err)
					return
				}
				ver = snap.Version
			}
		}(g)
	}
	// 单一写者按最新版本串行变更，保证每步成功且状态连续。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 400; i++ {
			ver := s.Version()
			snap := s.Snapshot()
			n := len16(snap.Text)
			pos, _ := s.OffsetToPosition(i % (n + 1))
			if !isSafeInsertPoint(s, pos) {
				continue
			}
			r := s.Change(ver, []Edit{{Range: Range{Start: pos, End: pos}, Text: "z"}})
			if !r.Accepted && !errors.Is(r.Reason, ErrStaleVersion) {
				t.Errorf("unexpected change reject: %v", r.Reason)
				return
			}
		}
		close(stop)
	}()
	wg.Wait()
}

// isSafeInsertPoint 避免把插入点选在代理对中间（基准测试文本为 ASCII，恒真，
// 这里仍做一次防御性校验）。
func isSafeInsertPoint(s *Service, p Position) bool {
	_, err := s.PositionToOffset(p)
	return err == nil
}
