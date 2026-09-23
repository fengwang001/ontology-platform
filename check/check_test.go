package check_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"

	"ontology/check"
	"ontology/sem"
	"ontology/waitq"
)

func waitFor(f func() bool) {
	for !f() {
		runtime.Gosched()
	}
}

func acquire(s *sem.Sem, n int64) <-chan error {
	c := make(chan error, 1)
	go func() { c <- s.Acquire(context.Background(), n) }()
	return c
}

// TestSentinels：哨兵错误彼此可区分；超大与超量立即返回。
func TestSentinels(t *testing.T) {
	s := sem.New(1, 1)
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"too_large", func() error { return s.Acquire(context.Background(), 2) }, sem.ErrTooLarge},
		{"over_release", func() error { return s.Release(1) }, sem.ErrOverRelease},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !errors.Is(c.call(), c.want) {
				t.Fatalf("%s: wrong sentinel", c.name)
			}
		})
	}
}

// TestSemantics：表驱动钉住第二节 1/2/3/4/6 条。
func TestSemantics(t *testing.T) {
	t.Run("fifo_no_bypass", func(t *testing.T) {
		s := sem.New(10, 4)
		if err := s.Acquire(context.Background(), 9); err != nil {
			t.Fatal(err)
		}
		b := acquire(s, 5)
		waitFor(func() bool { return s.Stats().Waiting == 1 })
		if s.TryAcquire(1) {
			t.Fatal("TryAcquire bypassed FIFO head")
		}
		if err := s.Release(9); err != nil || <-b != nil || !s.TryAcquire(5) {
			t.Fatal("FIFO/group wake broken")
		}
	})
	t.Run("group_wake_gap", func(t *testing.T) {
		s := sem.New(10, 8)
		_ = s.Acquire(context.Background(), 10)
		ns := []int64{6, 3, 3}
		done := map[int]<-chan error{}
		for i, n := range ns {
			done[i] = acquire(s, n)
		}
		waitFor(func() bool { return s.Stats().Waiting == 3 })
		if err := s.Release(10); err != nil {
			t.Fatal(err)
		}
		if <-done[0] != nil {
			t.Fatal("head must be granted")
		}
		select {
		case e := <-done[1]:
			t.Fatalf("waiter behind gap must stay blocked, got %v", e)
		default:
		}
		if s.Stats().Waiting != 2 {
			t.Fatal("wake must stop at first unsatisfiable waiter")
		}
	})
	t.Run("cancel_clean", func(t *testing.T) {
		s := sem.New(4, 4)
		ctx, cancel := context.WithCancel(context.Background())
		res := make(chan error, 1)
		go func() { res <- s.Acquire(ctx, 4) }()
		waitFor(func() bool { return s.Stats().Waiting == 1 })
		cancel()
		if !errors.Is(<-res, context.Canceled) {
			t.Fatal("cancel must return ctx.Err()")
		}
		if st := s.Stats(); st.Waiting != 0 || st.Used != 0 || !s.TryAcquire(4) {
			t.Fatal("cancel had side effects")
		}
	})
	t.Run("max_waiters", func(t *testing.T) {
		s := sem.New(1, 2)
		_ = s.Acquire(context.Background(), 1)
		stop := make(chan struct{})
		started := make(chan struct{}, 2)
		for range 2 {
			go func() {
				started <- struct{}{}
				_ = s.Acquire(context.Background(), 1)
				<-stop
			}()
			<-started
		}
		waitFor(func() bool { return s.Stats().Waiting == 1 })
		defer close(stop)
		if !errors.Is(s.Acquire(context.Background(), 1), sem.ErrTooManyWaiters) || s.TryAcquire(1) {
			t.Fatal("waiter cap or FIFO not enforced")
		}
	})
}

// TestRuleAHeadCancelWakes：只在 Release 唤醒的错误实现下该场景必失败。
func TestRuleAHeadCancelWakes(t *testing.T) {
	for range 50 {
		s := sem.New(10, 8)
		_ = s.Acquire(context.Background(), 9)
		ctx, cancel := context.WithCancel(context.Background())
		big := make(chan error, 1)
		go func() { big <- s.Acquire(ctx, 5) }()
		small := acquire(s, 1)
		waitFor(func() bool { return s.Stats().Waiting == 2 })
		cancel()
		if !errors.Is(<-big, context.Canceled) {
			t.Fatal("big should be canceled")
		}
		if <-small != nil {
			t.Fatal("rule A: head cancel must wake already-satisfiable follower")
		}
	}
}

// TestRuleBRaceVerdict：ctx 取消与满足同刻，已划额度必须改判成功且不泄漏。
func TestRuleBRaceVerdict(t *testing.T) {
	for range 2000 {
		s := sem.New(1, 1)
		ctx, cancel := context.WithCancel(context.Background())
		res := make(chan error, 1)
		go func() { res <- s.Acquire(ctx, 1) }()
		waitFor(func() bool { return s.Stats().Waiting == 1 })
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); cancel() }()
		go func() { defer wg.Done(); _ = s.Release(1) }()
		wg.Wait()
		if err := <-res; err != nil {
			t.Fatalf("granted-in-lock must win over cancel: %v", err)
		}
		if err := s.Release(1); err != nil || s.Stats().Used != 0 {
			t.Fatal("race verdict leaked quota")
		}
	}
}

// TestWaitQComplexity：两档规模取消触达节点数恒为常数。
func TestWaitQComplexity(t *testing.T) {
	for _, size := range []int{100, 100000} {
		q := &waitq.Queue{}
		ws := make([]*waitq.Waiter, size)
		t.Run(fmt.Sprintf("n=%d", size), func(t *testing.T) {
			for i := range size {
				ws[i] = waitq.NewWaiter(context.Background(), 1)
				q.Push(ws[i])
			}
			picks := map[string]int{"head": 0, "middle": size / 2, "tail": size - 1}
			for name, idx := range picks {
				q.ResetCounters()
				if !q.Remove(ws[idx]) || q.CancelChecks() > 2 {
					t.Fatalf("%s cancel touched %d nodes", name, q.CancelChecks())
				}
			}
		})
	}
}

// TestReleaseComplexity：Release 检查数 ≤ 被满足数 + 1。
func TestReleaseComplexity(t *testing.T) {
	for _, tc := range []struct {
		name       string
		size       int
		grant      int
		wantChecks int
	}{
		{"100_all", 100, 100, 100},
		{"100000_gap", 100000, 50000, 50001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sem.New(int64(tc.size), int64(tc.size+1))
			_ = s.Acquire(context.Background(), int64(tc.size))
			stops := make([]<-chan error, tc.size)
			for i := range tc.size {
				n := int64(1)
				if i >= tc.grant {
					n = 2 // 第一档：全部 1；第二档：后半 2，第 50001 个即缺口。
				}
				stops[i] = acquire(s, n)
			}
			waitFor(func() bool { return s.Stats().Waiting == tc.size })
			if err := s.Release(int64(tc.size)); err != nil {
				t.Fatal(err)
			}
			if got := s.ReleaseChecks(); got != tc.wantChecks {
				t.Fatalf("release checks=%d want %d", got, tc.wantChecks)
			}
		})
	}
}

// TestFaultInjection：四种取消位置 ×5000 轮，每轮后影子模型与账本一致。
func TestFaultInjection(t *testing.T) {
	cases := []string{"head", "middle", "tail", "cancel_with_release"}
	for round := range 5000 {
		for _, kind := range cases {
			s, sh := sem.New(10, 16), check.New(10)
			sh.Begin()
			_ = s.Acquire(context.Background(), 10)
			sh.Granted(10)
			sh.End()
			ctxs, cancels, res := []context.Context{}, []context.CancelFunc{}, []<-chan error{}
			ns := []int64{6, 3, 2, 1}
			for _, n := range ns {
				ctx, cancel := context.WithCancel(context.Background())
				ctxs, cancels = append(ctxs, ctx), append(cancels, cancel)
				c := make(chan error, 1)
				go func(ctx context.Context, n int64) { c <- s.Acquire(ctx, n) }(ctx, n)
				res = append(res, c)
			}
			waitFor(func() bool { return s.Stats().Waiting == 4 })
			pick := map[string]int{"head": 0, "middle": 2, "tail": 3, "cancel_with_release": 0}[kind]
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); cancels[pick]() }()
			go func() {
				defer wg.Done()
				if kind == "cancel_with_release" {
					_ = s.Release(10)
					sh.Begin(); sh.Returned(10); sh.End()
				}
			}()
			wg.Wait()
			for i, c := range res {
				err := <-c
				if err == nil {
					sh.Begin(); sh.Granted(ns[i]); sh.End()
					_ = s.Release(ns[i])
					sh.Begin(); sh.Returned(ns[i]); sh.End()
				}
			}
			if ok, msg := sh.Verify(s); !ok {
				t.Fatalf("round %d %s: %s", round, kind, msg)
			}
			for _, cancel := range cancels {
				cancel()
			}
		}
	}
}

// TestConcurrent：32 goroutine 随机权重获取/释放，含提前取消，逐步影子比对。
func TestConcurrent(t *testing.T) {
	const workers, rounds = 32, 200
	s, sh := sem.New(64, 128), check.New(64)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for r := range rounds {
				n := int64((id + r) % 12)
				ctx, cancel := context.WithCancel(context.Background())
				if r%7 == 0 {
					cancel() // 提前取消：未入队即返回，零副作用。
				}
				sh.Begin()
				err := s.Acquire(ctx, n)
				if err == nil {
					sh.Granted(n)
					runtime.Gosched()
					if e := s.Release(n); e != nil {
						t.Errorf("release: %v", e)
					}
					sh.Returned(n)
				}
				sh.End()
				cancel()
				if r%16 == 0 {
					if ok, msg := sh.Verify(s); !ok {
						t.Errorf("step verify: %s", msg)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()
	if ok, msg := sh.Verify(s); !ok {
		t.Fatal(msg)
	}
	if st := s.Stats(); st.Used != 0 || st.Waiting != 0 {
		t.Fatalf("end state not clean: %+v", st)
	}
}
