package pathlock

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentLockSamePath 验证多用户并发抢同一路径时恰好一个成功，
// 其余都收到「已被他人持有」。
func TestConcurrentLockSamePath(t *testing.T) {
	s := NewService()
	const n = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	var successes atomic.Int64
	codes := make(chan ErrorCode, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := s.Lock(fmt.Sprintf("user-%d", i), "shared/file.bin")
			if err == nil {
				successes.Add(1)
			} else {
				codes <- errCodeOf(err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(codes)
	heldByOther := 0
	for c := range codes {
		if c == ErrHeldByOther {
			heldByOther++
		}
	}
	t.Logf("输入=%d 用户并发抢同一路径 实际输出=成功 %d、已被他人持有 %d 判定依据=恰好一个成功",
		n, successes.Load(), heldByOther)
	if successes.Load() != 1 || heldByOther != n-1 {
		t.Errorf("成功=%d 他人持有=%d，期望 1 与 %d", successes.Load(), heldByOther, n-1)
	}
}

// TestConcurrentLockAncestorDescendant 验证并发锁互为祖先后代的路径时恰好一个成功。
func TestConcurrentLockAncestorDescendant(t *testing.T) {
	for round := 0; round < 200; round++ {
		s := NewService()
		start := make(chan struct{})
		var wg sync.WaitGroup
		var successes atomic.Int64
		var ancestorErr, descendantErr atomic.Value
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			if _, err := s.Lock("alice", "c/d"); err == nil {
				successes.Add(1)
			} else {
				ancestorErr.Store(errCodeOf(err))
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			if _, err := s.Lock("bob", "c/d/e"); err == nil {
				successes.Add(1)
			} else {
				descendantErr.Store(errCodeOf(err))
			}
		}()
		close(start)
		wg.Wait()
		if successes.Load() != 1 {
			t.Fatalf("第 %d 轮：成功数应为 1，得到 %d", round, successes.Load())
		}
		for _, v := range []atomic.Value{ancestorErr, descendantErr} {
			if got := v.Load(); got != nil && got.(ErrorCode) != ErrAncestorOrDescendantConflict {
				t.Fatalf("第 %d 轮：失败方应报祖先后代冲突，得到 %v", round, got)
			}
		}
	}
	t.Logf("输入=200 轮并发祖先后代抢锁 实际输出=每轮恰好一个成功 判定依据=排他性串行化")
}

// TestConcurrentMixedStress 混合并发压测：加锁、释放、校验、查询任意交错，
// 结束后核验服务内部状态自洽（配合 -race 检测数据竞争）。
func TestConcurrentMixedStress(t *testing.T) {
	s := NewService("admin")
	users := []string{"alice", "bob", "carol", "admin"}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 500; i++ {
				user := users[rng.Intn(len(users))]
				path := fmt.Sprintf("d%d/f%d", rng.Intn(4), rng.Intn(8))
				switch rng.Intn(4) {
				case 0:
					l, err := s.Lock(user, path)
					if err == nil {
						_ = s.Unlock(user, l.ID, rng.Intn(2) == 0)
					}
				case 1:
					_, _ = s.ValidatePush(user, []string{path}, rng.Intn(2) == 0)
				case 2:
					_, _, _, _ = s.ListByPrefix("d0", "", 4)
				case 3:
					_, _, _ = s.Lookup(path)
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()

	all, _, more, err := s.ListByPrefix("", "", 10000)
	if err != nil || more {
		t.Fatalf("终态列举失败: %v more=%v", err, more)
	}
	for _, l := range all {
		got, ok, _ := s.Lookup(l.Path)
		if !ok || got.ID != l.ID || got.Owner != l.Owner {
			t.Fatalf("终态不自洽: 列表 %+v 查询 %+v", l, got)
		}
	}
	t.Logf("输入=8 协程混合并发 500 次操作 实际输出=终态 %d 把锁且逐一自洽 判定依据=线性一致性",
		len(all))
}
