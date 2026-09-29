package di

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestScopedWaiterFailureThenRetry(t *testing.T) {
	log := &orderLog{}
	scCtl := &ctl{log: log}
	badCtl := &ctl{log: log, failAt: 1, err: errors.New("nope")}
	c := New()
	// outer(transient) 依赖 sc(作用域) 与 bad；sc 先成功，bad 失败，整批回滚。
	mustReg(t, c, Registration{Name: "outer", Lifetime: Transient, Dependencies: []string{"sc", "bad"}, Construct: (&ctl{log: log}).ctor("outer")})
	mustReg(t, c, Registration{Name: "sc", Lifetime: Scoped, Construct: scCtl.ctor("sc")})
	mustReg(t, c, Registration{Name: "bad", Lifetime: Transient, Construct: badCtl.ctor("bad")})
	mustFreeze(t, c)
	sc, _ := c.NewScope("s")

	const waiters = 8
	var wg sync.WaitGroup
	errs := make([]error, waiters)
	start := make(chan struct{})
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = sc.Resolve("sc" /*直接解析 sc：它不依赖 bad*/)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("waiter %d: %v", i, err)
		}
	}

	// 另一个批次：outer 失败导致其图中（其实不经过 sc 共享？outer 直接依赖 sc）
	// 注意 outer 与 sc 同图时 sc 会成功发布后随 outer 失败回滚。
	if _, err := sc.Resolve("outer"); err == nil {
		t.Fatal("expected outer failure")
	}
	_, dis := log.snapshot()
	scReleases := 0
	for _, n := range dis {
		if n == "sc" {
			scReleases++
		}
	}
	if scReleases != 0 {
		t.Fatalf("previously cached sc must survive unrelated failed batch, releases=%d", scReleases)
	}

	// 修复 bad 后，outer 成功，sc 复用缓存不再重建
	atomic.StoreInt32(&badCtl.failAt, 0)
	if _, err := sc.Resolve("outer"); err != nil {
		t.Fatalf("retry outer: %v", err)
	}
	if got := atomic.LoadInt32(&scCtl.count); got != 1 {
		t.Fatalf("sc constructed %d times, want 1 (cached reused)", got)
	}
	t.Logf("input failed batch after successful scoped resolution -> dispose=%v | 判定依据: 已提交作用域实例不被无关失败回滚", dis)
}
