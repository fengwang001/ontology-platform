package srp

import (
	"sync"
	"testing"
)

// TestConcurrent 多 goroutine 并发调用全部操作与查询；
// 结果等价于某一串行顺序由互斥锁保证，-race 下验证无数据竞争。
func TestConcurrent(t *testing.T) {
	s := New()
	if err := s.DeclareResource("R", 8); err != nil {
		t.Fatal(err)
	}
	for i, d := range []int{100, 80, 60, 40, 20} {
		id := string(rune('A' + i))
		if err := s.AddTask(id, d, Mu{"R", 8}); err != nil {
			t.Fatal(err)
		}
	}

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

	tasks := []string{"A", "B", "C", "D", "E"}
	jobN := 0
	var jmu sync.Mutex

	wg.Add(5)
	// 混合操作 worker：合法/非法操作都会被拒绝处理，不 panic 即可。
	for w := 0; w < 4; w++ {
		go worker(func() {
			jmu.Lock()
			n := jobN
			jobN = n + 1
			jmu.Unlock()
			jid := "p" + itoaSafe(n)
			tid := tasks[n%len(tasks)]
			if err := s.Start(jid, tid); err == nil {
				_ = s.Acquire(jid, "R", 1)
				_ = s.Release(jid, "R", 1)
				_ = s.Finish(jid)
			}
		})
	}
	go worker(func() {
		_ = s.SysCeil()
		_, _ = s.Ceil("R")
		_, _ = s.Avail("R")
		_ = s.Stack()
		_, _ = s.Level("A")
	})

	// 跑约 50ms 的混合流量。
	for i := 0; i < 50; i++ {
		assertPublicInvariant(t, s, "concurrent-quiescent-check")
	}
	close(stop)
	wg.Wait()
	assertPublicInvariant(t, s, "after-concurrent")
	// 忙等 worker 可能停在任意合法中间点，只要求守恒与栈层级不变量；
	// 这里把残留作业（若有）顺序清理，证明它们都处于可恢复的一致状态。
	for {
		st := s.Stack()
		if len(st) == 0 {
			break
		}
		top := st[len(st)-1]
		s.mu.Lock()
		held := map[string]int{}
		for r, h := range s.jobs[top.ID].held {
			held[r] = h
		}
		s.mu.Unlock()
		for r, h := range held {
			if err := s.Release(top.ID, r, h); err != nil {
				t.Fatalf("cleanup release: %v", err)
			}
		}
		if err := s.Finish(top.ID); err != nil {
			t.Fatalf("cleanup finish: %v", err)
		}
	}
}

// assertPublicInvariant 仅通过加锁的公开/包内访问器校验，适合并发场景。
func assertPublicInvariant(t *testing.T, s *SRP, op string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, r := range s.resources {
		held := 0
		for _, j := range s.stack {
			held += j.held[id]
		}
		if r.avail+held != r.n {
			t.Fatalf("after %s: %s avail+held=%d != N=%d", op, id, r.avail+held, r.n)
		}
	}
	for i := 1; i < len(s.stack); i++ {
		if s.levelLocked(s.stack[i].task) <= s.levelLocked(s.stack[i-1].task) {
			t.Fatalf("after %s: stack pi not strictly increasing", op)
		}
	}
}

func itoaSafe(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
