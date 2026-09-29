package di

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentFirstSingletonResolution(t *testing.T) {
	const n = 100
	log := &orderLog{}
	var started int32
	release := make(chan struct{})
	sCtl := &ctl{log: log}
	c := New()
	mustReg(t, c, Registration{
		Name:     "s",
		Lifetime: Singleton,
		Construct: func(deps map[string]any) (any, error) {
			atomic.AddInt32(&started, 1)
			<-release // 阻塞构造，放大并发窗口
			return sCtl.ctor("s")(deps)
		},
	})
	mustFreeze(t, c)

	var wg sync.WaitGroup
	results := make([]any, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			v, err := c.Resolve("s")
			results[i] = v
			errs[i] = err
		}(i)
	}
	close(start)
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&started); got != 1 {
		t.Fatalf("constructor started %d times while in flight, want exactly 1", got)
	}
	close(release)
	wg.Wait()

	var first any
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("resolver %d got error: %v", i, errs[i])
		}
		if first == nil {
			first = results[i]
		} else if results[i] != first {
			t.Fatalf("resolver %d got a different instance", i)
		}
	}
	if got := atomic.LoadInt32(&sCtl.count); got != 1 {
		t.Fatalf("singleton constructor completed %d times, want 1", got)
	}
	t.Logf("input %d concurrent first resolutions -> output one instance, constructor calls=1 | 判定依据: 单飞 + 缓存", n)
}

func TestConcurrentSingletonFailureSharesError(t *testing.T) {
	const n = 50
	sentinel := errors.New("sentinel construct failure")
	var calls int32
	release := make(chan struct{})
	c := New()
	mustReg(t, c, Registration{
		Name:     "s",
		Lifetime: Singleton,
		Construct: func(deps map[string]any) (any, error) {
			atomic.AddInt32(&calls, 1)
			<-release
			return nil, sentinel
		},
	})
	mustFreeze(t, c)

	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = c.Resolve("s")
		}(i)
	}
	close(start)
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if !errors.Is(err, sentinel) {
			t.Fatalf("resolver %d got %v, want shared sentinel", i, err)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("failed constructor called %d times, want 1", got)
	}
	// 失败后下一批解析重新构造一次
	if _, err := c.Resolve("s"); !errors.Is(err, sentinel) {
		t.Fatalf("post-failure resolve got %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("constructor called %d times after batch failure, want 2", got)
	}
	t.Logf("input %d concurrent failing resolutions -> output same error, calls=1; retry calls=2 | 判定依据: 同批等待者同一错误, 失败不缓存", n)
}

func TestCloseConcurrentWithResolve(t *testing.T) {
	log := &orderLog{}
	scCtl := &ctl{log: log}
	hold := make(chan struct{})
	proceed := make(chan struct{})
	var inFlight int32
	c := New()
	mustReg(t, c, Registration{Name: "sc", Lifetime: Scoped, Construct: func(deps map[string]any) (any, error) {
		if atomic.AddInt32(&inFlight, 1) == 1 {
			close(hold)
			<-proceed // 让首个在途解析跨越关闭调用
		}
		return scCtl.ctor("sc")(deps)
	}})
	mustFreeze(t, c)
	sc, _ := c.NewScope("s")

	const resolvers = 32
	var wg sync.WaitGroup
	results := make([]string, resolvers)
	start := make(chan struct{})
	for i := 0; i < resolvers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			v, err := sc.Resolve("sc")
			if err != nil {
				if errors.Is(err, ErrScopeClosed) || errors.Is(err, ErrContainerClosed) {
					results[i] = "rejected"
					return
				}
				results[i] = "other-error"
				return
			}
			_ = v
			results[i] = "resolved"
		}(i)
	}
	close(start)
	<-hold // 至少一个在途解析正在构造

	closeDone := make(chan struct{})
	go func() {
		_ = sc.Close()
		close(closeDone)
	}()

	// 关闭必须等在途解析结束：给它一点时间，确认它尚未释放实例
	select {
	case <-closeDone:
		t.Fatal("scope closed before in-flight resolution finished")
	case <-time.After(50 * time.Millisecond):
	}
	close(proceed)
	wg.Wait()
	<-closeDone

	var resolved, rejected int
	for _, r := range results {
		switch r {
		case "resolved":
			resolved++
		case "rejected":
			rejected++
		default:
			t.Fatalf("unexpected result %q", r)
		}
	}
	if resolved == 0 {
		t.Fatal("at least the in-flight resolution should succeed")
	}

	// 关闭后开始的解析必须被拒
	if _, err := sc.Resolve("sc"); !errors.Is(err, ErrScopeClosed) && !errors.Is(err, ErrContainerClosed) {
		t.Fatalf("post-close resolve got %v", err)
	}

	// 每个被构造出的实例恰好释放一次
	_, dis := log.snapshot()
	constructed := atomic.LoadInt32(&scCtl.count)
	if int32(len(dis)) != constructed {
		t.Fatalf("disposed %d instances but constructed %d: %v", len(dis), constructed, dis)
	}
	counts := map[string]int{}
	for _, n := range dis {
		counts[n]++
		if counts[n] > 1 {
			t.Fatalf("instance disposed twice: %v", dis)
		}
	}
	t.Logf("input close concurrent with %d resolves -> output resolved=%d rejected=%d, constructed=%d, dispose=%v | 判定依据: 关闭等待在途解析, 其后解析拒绝, 每实例释放一次",
		resolvers, resolved, rejected, constructed, dis)
}

func TestContainerCloseConcurrentWithScopedResolve(t *testing.T) {
	log := &orderLog{}
	scCtl := &ctl{log: log}
	hold := make(chan struct{})
	proceed := make(chan struct{})
	var started int32
	c := New()
	mustReg(t, c, Registration{Name: "sc", Lifetime: Scoped, Construct: func(deps map[string]any) (any, error) {
		if atomic.AddInt32(&started, 1) == 1 {
			close(hold)
			<-proceed
		}
		return scCtl.ctor("sc")(deps)
	}})
	mustFreeze(t, c)

	const scopes = 16
	var wg sync.WaitGroup
	ok, bad := int32(0), int32(0)
	begin := make(chan struct{})
	for i := 0; i < scopes; i++ {
		sc, err := c.NewScope(fmt.Sprintf("s%d", i))
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-begin
			if _, err := sc.Resolve("sc"); err != nil {
				atomic.AddInt32(&bad, 1)
			} else {
				atomic.AddInt32(&ok, 1)
			}
		}()
	}
	close(begin)
	<-hold
	closed := make(chan struct{})
	go func() { _ = c.Close(); close(closed) }()
	time.Sleep(50 * time.Millisecond)
	close(proceed)
	wg.Wait()
	<-closed

	_, dis := log.snapshot()
	constructed := atomic.LoadInt32(&scCtl.count)
	if int32(len(dis)) != constructed {
		t.Fatalf("disposed %d, constructed %d: %v", len(dis), constructed, dis)
	}
	t.Logf("input container close vs %d scope resolves -> ok=%d rejected=%d, dispose count=%d | 判定依据: 容器关闭等待全部在途解析并先关作用域",
		scopes, atomic.LoadInt32(&ok), atomic.LoadInt32(&bad), len(dis))
}
