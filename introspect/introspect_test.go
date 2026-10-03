package introspect

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestFreshnessBoundaryAndFailureNoExtend(t *testing.T) {
	// P=10 N=5 G=20；正向记录 Exp=100，故 u=min(0+10,100)=10。
	var calls int64
	f := func(token string) (Result, error) {
		n := atomic.AddInt64(&calls, 1)
		switch n {
		case 1:
			return Result{Active: true, Sub: "u1", Iat: 0, Exp: 100, Scopes: []string{"orders"}}, nil
		case 2:
			return Result{}, errors.New("boom") // now=10：失败不得延长鲜期
		case 3:
			return Result{}, errors.New("boom") // now=11：缓存依旧陈旧
		default:
			t.Fatalf("unexpected call #%d", n)
			return Result{}, nil
		}
	}
	c := New(10, 5, 20, f)

	e, src, err := c.Obtain("t1", 0)
	check := func(now int64, wantSrc Source, wantCalls int64, wantErr bool) Entry {
		t.Helper()
		e, src, err = c.Obtain("t1", now)
		if (err != nil) != wantErr {
			t.Fatalf("now=%d err=%v wantErr=%v", now, err, wantErr)
		}
		if src != wantSrc {
			t.Fatalf("now=%d src=%d want %d", now, src, wantSrc)
		}
		if got := c.Calls(); got != wantCalls {
			t.Fatalf("now=%d calls=%d want %d", now, got, wantCalls)
		}
		return e
	}
	if err != nil || src != SourceFresh || e.U != 10 {
		t.Fatalf("initial: e=%+v src=%d err=%v", e, src, err)
	}
	check(9, SourceCache, 1, false) // now < u 仍新鲜
	old := check(10, 0, 2, true)    // 恰在 u 失效，f 失败；旧记录随 err 返回
	if !old.Res.Active || old.U != 10 {
		t.Fatalf("old entry corrupted: %+v", old)
	}
	old = check(11, 0, 3, true) // f 再次失败，鲜期未被延长
	if old.F0 != 0 {
		t.Fatalf("f failure must not extend/refresh record: f0=%d", old.F0)
	}
}

func TestNegativeRecordFreshness(t *testing.T) {
	var calls int64
	f := func(token string) (Result, error) {
		atomic.AddInt64(&calls, 1)
		return Result{Active: false}, nil
	}
	c := New(10, 5, 20, f)
	e, src, err := c.Obtain("t2", 0)
	if err != nil || src != SourceFresh || e.U != 5 || e.Res.Active {
		t.Fatalf("negative initial: %+v src=%d err=%v", e, src, err)
	}
	if _, src, _ := c.Obtain("t2", 4); src != SourceCache { // now=4 < u=5
		t.Fatalf("now=4 expected Cache, got %d", src)
	}
	if c.Calls() != 1 {
		t.Fatalf("cache hit must not call f, calls=%d", c.Calls())
	}
	if _, src, _ := c.Obtain("t2", 5); src != SourceFresh { // now==u 失效并重新调用
		t.Fatalf("now=5 expected Fresh, got %d", src)
	}
	if c.Calls() != 2 {
		t.Fatalf("calls=%d want 2", c.Calls())
	}
}

func TestSingleflightConcurrentMiss(t *testing.T) {
	const waiters = 32
	var calls int64
	release := make(chan struct{})
	f := func(token string) (Result, error) {
		atomic.AddInt64(&calls, 1)
		<-release
		return Result{Active: true, Sub: "u1", Iat: 0, Exp: 100}, nil
	}
	c := New(10, 5, 20, f)

	var wg sync.WaitGroup
	errs := make([]error, waiters)
	srcs := make([]Source, waiters)
	started := make(chan struct{})
	for i := 1; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, srcs[i], errs[i] = c.Obtain("t", 0)
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, srcs[0], errs[0] = c.Obtain("t", 0)
	}()
	go func() {
		for c.Waiters("t") != waiters {
			runtime.Gosched()
		}
		close(started)
	}()
	<-started
	close(release)
	wg.Wait()
	if c.Calls() != 1 {
		t.Fatalf("concurrent miss produced %d upstream calls, want 1", c.Calls())
	}
	for i := 0; i < waiters; i++ {
		if errs[i] != nil || srcs[i] != SourceFresh {
			// 等待者共用发起者记录，来源记为 Fresh
			t.Fatalf("waiter %d: src=%d err=%v", i, srcs[i], errs[i])
		}
	}
}

func TestSingleflightFailureEachWaiterGetsOldEntry(t *testing.T) {
	const waiters = 8
	var calls int64
	release := make(chan struct{})
	f := func(token string) (Result, error) {
		n := atomic.AddInt64(&calls, 1)
		if n == 1 {
			return Result{Active: true, Sub: "u1", Iat: 0, Exp: 100}, nil
		}
		<-release // 后续唯一一次失败调用在此阻塞，等待者单飞加入
		return Result{}, errors.New("upstream down")
	}
	c := New(10, 5, 20, f)
	// 预置一条正向记录
	seed, _, err := c.Obtain("t", 0)
	if err != nil || seed.U != 10 {
		t.Fatalf("seed failed: %+v err=%v", seed, err)
	}
	oldEntries := make([]Entry, waiters)
	var wg sync.WaitGroup
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			oldEntries[i], _, _ = c.Obtain("t", 10)
		}(i)
	}
	for c.Waiters("t") != waiters {
		runtime.Gosched()
	}
	close(release)
	wg.Wait()
	if got := c.Calls(); got != 2 {
		t.Fatalf("calls=%d want 2 (seed + one shared failure)", got)
	}
	for i, e := range oldEntries {
		if !e.Res.Active || e.F0 != 0 || e.U != 10 {
			t.Fatalf("waiter %d got wrong old entry: %+v", i, e)
		}
	}
}
