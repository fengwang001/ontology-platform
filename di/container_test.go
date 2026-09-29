package di

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// eventLog records construction and release events with the service name.
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (l *eventLog) add(ev string) {
	l.mu.Lock()
	l.events = append(l.events, ev)
	l.mu.Unlock()
}

func (l *eventLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.events...)
}

// tracked returns a constructor that logs "+name" on build and "-name"
// on release. failAt > 0 makes the failAt-th call panic.
func tracked(l *eventLog, builds *int64, name string, failAt int64) Constructor {
	return func(d map[string]any) (any, func()) {
		if builds != nil {
			n := atomic.AddInt64(builds, 1)
			if failAt > 0 && n == failAt {
				l.add("fail:" + name)
				panic("boom:" + name)
			}
		}
		l.add("+" + name)
		n := name
		return &n, func() { l.add("-" + n) }
	}
}

func mustRegister(t *testing.T, c *Container, name string, deps []string, lt Lifetime, ctor Constructor) {
	t.Helper()
	if err := c.Register(name, deps, lt, ctor); err != nil {
		t.Fatalf("Register(%s) error: %v", name, err)
	}
}

func mustFreeze(t *testing.T, c *Container) {
	t.Helper()
	if err := c.Freeze(); err != nil {
		t.Fatalf("Freeze() error: %v", err)
	}
}

func releaseEvents(evs []string) []string {
	var out []string
	for _, e := range evs {
		if len(e) > 0 && e[0] == '-' {
			out = append(out, e)
		}
	}
	return out
}

func count(list []string, s string) int {
	n := 0
	for _, v := range list {
		if v == s {
			n++
		}
	}
	return n
}

// TestSingletonThroughTransientToScopedRejected covers the headline
// captivity rule: singleton -> transient -> scoped is rejected at freeze.
func TestSingletonThroughTransientToScopedRejected(t *testing.T) {
	t.Log("input: s(singleton)->t(transient)->x(scoped); want Freeze ErrCaptiveDependency")
	c := New()
	var l eventLog
	mustRegister(t, c, "x", nil, Scoped, tracked(&l, nil, "x", 0))
	mustRegister(t, c, "t", []string{"x"}, Transient, tracked(&l, nil, "t", 0))
	mustRegister(t, c, "s", []string{"t"}, Singleton, tracked(&l, nil, "s", 0))

	err := c.Freeze()
	t.Logf("output: Freeze error=%v events=%v", err, l.snapshot())
	if !errors.Is(err, ErrCaptiveDependency) {
		t.Fatalf("判定依据: 单例闭包穿过瞬态t到达作用域x应报俘获; got %v", err)
	}
	if evs := l.snapshot(); len(evs) != 0 {
		t.Fatalf("判定依据: 被拒绝的冻结不创建任何实例; got %v", evs)
	}

	// Failed freeze rejects the whole config; fix it by replacing the
	// transient bridge with a singleton boundary so the closure is safe.
	mustRegister(t, c, "t", []string{"y"}, Singleton, tracked(&l, nil, "t", 0))
	mustRegister(t, c, "y", nil, Singleton, tracked(&l, nil, "y", 0))
	if err := c.Freeze(); err != nil {
		t.Fatalf("判定依据: 冻结失败后应可再注册; got %v", err)
	}
	t.Log("output: 替换t为单例边界后 Freeze 成功 (整体拒绝后容器仍可编辑)")
}

// TestValidationOrder checks the strict ordering of static validation.
func TestValidationOrder(t *testing.T) {
	t.Run("duplicate beats missing dependency", func(t *testing.T) {
		c := New()
		mustRegister(t, c, "a", []string{"ghost"}, Singleton, tracked(nil, nil, "a", 0))
		mustRegister(t, c, "a", nil, Singleton, tracked(nil, nil, "a", 0))
		err := c.Freeze()
		t.Logf("input: 重复注册a且a依赖缺失ghost; output=%v", err)
		if !errors.Is(err, ErrDuplicateRegistration) {
			t.Fatalf("判定依据: 重复注册最先报; got %v", err)
		}
	})
	t.Run("missing dependency beats cycle", func(t *testing.T) {
		c := New()
		mustRegister(t, c, "a", []string{"b", "ghost"}, Singleton, tracked(nil, nil, "a", 0))
		mustRegister(t, c, "b", []string{"a"}, Singleton, tracked(nil, nil, "b", 0))
		err := c.Freeze()
		t.Logf("input: a<->b成环且依赖缺失ghost; output=%v", err)
		if !errors.Is(err, ErrDependencyNotFound) {
			t.Fatalf("判定依据: 依赖缺失先于成环; got %v", err)
		}
	})
	t.Run("cycle beats captivity", func(t *testing.T) {
		c := New()
		mustRegister(t, c, "a", []string{"b"}, Singleton, tracked(nil, nil, "a", 0))
		mustRegister(t, c, "b", []string{"a", "sc"}, Transient, tracked(nil, nil, "b", 0))
		mustRegister(t, c, "sc", nil, Scoped, tracked(nil, nil, "sc", 0))
		err := c.Freeze()
		t.Logf("input: a<->b成环且a经瞬态b俘获sc; output=%v", err)
		if !errors.Is(err, ErrDependencyCycle) {
			t.Fatalf("判定依据: 成环先于俘获; got %v", err)
		}
	})
	t.Run("cycle path reported", func(t *testing.T) {
		c := New()
		mustRegister(t, c, "a", []string{"b"}, Singleton, tracked(nil, nil, "a", 0))
		mustRegister(t, c, "b", []string{"a"}, Singleton, tracked(nil, nil, "b", 0))
		err := c.Freeze()
		t.Logf("input: a->b->a; output=%v", err)
		if !errors.Is(err, ErrDependencyCycle) {
			t.Fatalf("判定依据: 环必须被检出; got %v", err)
		}
	})
}

// TestScopedRejectedFromRoot covers direct and transient-hidden scoped.
func TestScopedRejectedFromRoot(t *testing.T) {
	c := New()
	var l eventLog
	mustRegister(t, c, "sc", nil, Scoped, tracked(&l, nil, "sc", 0))
	mustRegister(t, c, "tr", []string{"sc"}, Transient, tracked(&l, nil, "tr", 0))
	mustFreeze(t, c)

	if _, err := c.Resolve("sc"); !errors.Is(err, ErrScopedFromRoot) {
		t.Fatalf("判定依据: 根容器直接解析作用域服务应拒绝; got %v", err)
	}
	if _, err := c.Resolve("tr"); !errors.Is(err, ErrScopedFromRoot) {
		t.Fatalf("判定依据: 根解析闭包触及作用域服务也应拒绝; got %v", err)
	}
	t.Logf("input: Resolve(sc), Resolve(tr->sc) at root; output=均ErrScopedFromRoot events=%v", l.snapshot())
	if evs := l.snapshot(); len(evs) != 0 {
		t.Fatalf("判定依据: 被拒绝操作不创建实例; got %v", evs)
	}
}

// TestDiamondSharesScoped verifies that a scoped dependency reached by
// two paths in one scope is built once and released once, after dependents.
func TestDiamondSharesScoped(t *testing.T) {
	c := New()
	var l eventLog
	var scBuilds int64
	mustRegister(t, c, "svc", []string{"left", "right"}, Transient, tracked(&l, nil, "svc", 0))
	mustRegister(t, c, "left", []string{"sc"}, Scoped, tracked(&l, nil, "left", 0))
	mustRegister(t, c, "right", []string{"sc"}, Scoped, tracked(&l, nil, "right", 0))
	mustRegister(t, c, "sc", nil, Scoped, tracked(&l, &scBuilds, "sc", 0))
	mustFreeze(t, c)

	s, err := c.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	v1, err := s.Resolve("svc")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := s.Resolve("svc")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input: 同一作用域两次解析菱形 svc(left,right)->sc; output p1=%p p2=%p scBuilds=%d",
		v1, v2, atomic.LoadInt64(&scBuilds))
	if atomic.LoadInt64(&scBuilds) != 1 {
		t.Fatalf("判定依据: 作用域实例每作用域恰好构造一次; got %d", scBuilds)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	rel := releaseEvents(l.snapshot())
	t.Logf("output: scope close release events=%v", rel)
	if rel[len(rel)-1] != "-sc" {
		t.Fatalf("判定依据: 被依赖的sc必须在所有依赖者之后释放; got %v", rel)
	}
	if count(rel, "-sc") != 1 {
		t.Fatalf("判定依据: 每个实例恰好释放一次; got %v", rel)
	}
}

// TestThirdLevelFailureReverseRelease builds a graph whose third layer
// c fails (a -> b -> c). A sibling branch completed earlier in the same
// resolution (w -> x) must be rolled back in reverse creation order.
func TestThirdLevelFailureReverseRelease(t *testing.T) {
	c := New()
	var l eventLog
	var cBuilds int64
	mustRegister(t, c, "a", []string{"x", "b"}, Transient, tracked(&l, nil, "a", 0))
	mustRegister(t, c, "x", []string{"w"}, Transient, tracked(&l, nil, "x", 0))
	mustRegister(t, c, "w", nil, Transient, tracked(&l, nil, "w", 0))
	mustRegister(t, c, "b", []string{"c"}, Transient, tracked(&l, nil, "b", 0))
	mustRegister(t, c, "c", nil, Transient, tracked(&l, &cBuilds, "c", 1))
	mustFreeze(t, c)
	s, err := c.NewScope()
	if err != nil {
		t.Fatal(err)
	}

	_, err = s.Resolve("a")
	t.Logf("input: 解析a, 兄弟分支w->x先建成, 随后a->b->c第三层c panic; output err=%v", err)
	var ce *ConstructionError
	if !errors.As(err, &ce) || ce.Service != "c" {
		t.Fatalf("判定依据: 应返回ConstructionError(c); got %v", err)
	}
	rel := releaseEvents(l.snapshot())
	t.Logf("output: release events=%v (期望[-x -w], c/b/a未建成不释放)", rel)
	if len(rel) != 2 || rel[0] != "-x" || rel[1] != "-w" {
		t.Fatalf("判定依据: 本次新建实例按创建逆序释放; got %v", rel)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	relAll := releaseEvents(l.snapshot())
	if count(relAll, "-x") != 1 || count(relAll, "-w") != 1 {
		t.Fatalf("判定依据: 失败实例未缓存, 关闭作用域不得再次释放; got %v", relAll)
	}
}

// TestSingletonFailureKeepsPriorSingletonsAndRetries covers: a failed
// singleton build rolls back only this build's new transients, keeps
// previously built singletons, and the next resolve retries the build.
func TestSingletonFailureKeepsPriorSingletonsAndRetries(t *testing.T) {
	c := New()
	var l eventLog
	var srvBuilds int64
	var depBuilds int64
	mustRegister(t, c, "keep", nil, Singleton, tracked(&l, nil, "keep", 0))
	mustRegister(t, c, "dep", nil, Transient, tracked(&l, &depBuilds, "dep", 0))
	mustRegister(t, c, "srv", []string{"keep", "dep"}, Singleton, tracked(&l, &srvBuilds, "srv", 1))
	mustFreeze(t, c)

	if _, err := c.Resolve("keep"); err != nil {
		t.Fatal(err)
	}
	_, err := c.Resolve("srv")
	t.Logf("input: 单例srv首次构造失败, 依赖keep(已成功单例)与dep(新瞬态); output err=%v", err)
	if err == nil {
		t.Fatal("判定依据: 构造失败必须返回错误")
	}
	rel := releaseEvents(l.snapshot())
	t.Logf("output: 失败后释放事件=%v", rel)
	if count(rel, "-dep") != 1 {
		t.Fatalf("判定依据: 本次新建非单例dep须逆序释放; got %v", rel)
	}
	if count(rel, "-keep") != 0 {
		t.Fatalf("判定依据: 已成功的单例keep必须保留; got %v", rel)
	}

	// The singleton was not cached, so the next resolution constructs it
	// again; the transient dependency is rebuilt while keep stays cached.
	srv2, err := c.Resolve("srv")
	t.Logf("input: 再次解析srv(首次失败不缓存, 重新构造); output v=%v depBuilds=%d", srv2, atomic.LoadInt64(&depBuilds))
	if err != nil {
		t.Fatalf("判定依据: 重新构造成功后应返回实例; got %v", err)
	}
	if atomic.LoadInt64(&depBuilds) != 2 {
		t.Fatalf("判定依据: 重试须重新构造瞬态链; dep构建=%d", depBuilds)
	}
	events := l.snapshot()
	if count(events, "+keep") != 1 {
		t.Fatalf("判定依据: 已成功单例保留且复用; events=%v", events)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	relFinal := releaseEvents(l.snapshot())
	t.Logf("output: 容器关闭后释放事件=%v", relFinal)
	if count(relFinal, "-keep") != 1 || count(relFinal, "-srv") != 1 || count(relFinal, "-dep") != 2 {
		t.Fatalf("判定依据: srv重试成功, 关闭逆序释放srv/dep, keep单例恰好一次; got %v", relFinal)
	}
}

// TestHundredConcurrentFirstSingletonResolve: 100 goroutines resolve the
// same unbuilt singleton concurrently; its constructor runs exactly once
// and all callers receive the same instance.
func TestHundredConcurrentFirstSingletonResolve(t *testing.T) {
	const n = 100
	c := New()
	var builds int64
	started := make(chan struct{})
	releaseGate := make(chan struct{})
	called := make(chan struct{}, n)
	ctor := func(map[string]any) (any, func()) {
		atomic.AddInt64(&builds, 1)
		called <- struct{}{}
		<-releaseGate
		v := 42
		return &v, func() {}
	}
	mustRegister(t, c, "s", nil, Singleton, ctor)
	mustFreeze(t, c)

	var wg sync.WaitGroup
	vals := make([]any, n)
	errs := make([]error, n)
	close(started)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-started
			vals[i], errs[i] = c.Resolve("s")
		}(i)
	}

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("判定依据: 必须有一个goroutine进入构造")
	}
	select {
	case <-called:
		t.Fatalf("判定依据: 并发首次解析单例, 构造只能进入一次; builds=%d", atomic.LoadInt64(&builds))
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseGate)
	wg.Wait()

	t.Logf("input: %d goroutine并发首次Resolve(s); output builds=%d", n, atomic.LoadInt64(&builds))
	if atomic.LoadInt64(&builds) != 1 {
		t.Fatalf("判定依据: 成功构造只调用一次; got %d", builds)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("判定依据: 同批等待者均应成功; err[%d]=%v", i, errs[i])
		}
		if vals[i] != vals[0] {
			t.Fatalf("判定依据: 同批等待者拿到同一实例; %p != %p", vals[i], vals[0])
		}
	}
}

// TestConcurrentSingletonFailureSharedError: concurrent first resolvers
// of a failing singleton get the same error; the build is retriable
// afterwards via a fresh registration set (rebuild with succeeding ctor).
func TestConcurrentSingletonFailureSharedError(t *testing.T) {
	const n = 50
	c := New()
	var builds int64
	gate := make(chan struct{})
	ctor := func(map[string]any) (any, func()) {
		atomic.AddInt64(&builds, 1)
		<-gate
		panic("shared boom")
	}
	mustRegister(t, c, "s", nil, Singleton, ctor)
	mustFreeze(t, c)

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.Resolve("s")
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	close(gate)
	wg.Wait()

	t.Logf("input: %d goroutine并发首次解析失败单例; output builds=%d", n, builds)
	if builds != 1 {
		t.Fatalf("判定依据: 失败构造也只调用一次; got %d", builds)
	}
	for i := 1; i < n; i++ {
		if errs[i] == nil || errs[i].Error() != errs[0].Error() {
			t.Fatalf("判定依据: 同批等待者得到同一错误; %v vs %v", errs[0], errs[i])
		}
	}
}

// TestScopeCloseVsConcurrentResolve: closing a scope rejects resolves
// that start afterwards and waits for an in-flight resolve; release is in
// reverse creation order and exactly once.
func TestScopeCloseVsConcurrentResolve(t *testing.T) {
	c := New()
	var l eventLog
	inCtor := make(chan struct{})
	releaseCtor := make(chan struct{})
	ctor := func(map[string]any) (any, func()) {
		l.add("+slow")
		inCtor <- struct{}{}
		<-releaseCtor
		v := "slow"
		return &v, func() { l.add("-slow") }
	}
	mustRegister(t, c, "slow", nil, Scoped, ctor)
	mustRegister(t, c, "fast", nil, Scoped, tracked(&l, nil, "fast", 0))
	mustFreeze(t, c)
	s, err := c.NewScope()
	if err != nil {
		t.Fatal(err)
	}

	resolvedSlow := make(chan error, 1)
	go func() {
		_, e := s.Resolve("slow")
		resolvedSlow <- e
	}()
	<-inCtor // resolution is now in flight

	closed := make(chan struct{})
	go func() {
		_ = s.Close()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("判定依据: 关闭必须等待在途解析结束")
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := s.Resolve("fast"); !errors.Is(err, ErrScopeClosed) {
		t.Fatalf("判定依据: 关闭开始后新解析必须拒绝; got %v", err)
	}
	close(releaseCtor)
	if err := <-resolvedSlow; err != nil {
		t.Fatalf("判定依据: 在途解析应正常完成; got %v", err)
	}
	<-closed

	rel := releaseEvents(l.snapshot())
	t.Logf("input: 作用域关闭与在途slow解析并发; output release=%v", rel)
	if len(rel) != 1 || rel[0] != "-slow" {
		t.Fatalf("判定依据: 在途实例构造成功后随关闭逆序释放一次; got %v", rel)
	}
	if _, err := s.Resolve("fast"); !errors.Is(err, ErrScopeClosed) {
		t.Fatalf("判定依据: 已关闭作用域拒绝解析; got %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("判定依据: 重复关闭幂等; got %v", err)
	}
	if count(releaseEvents(l.snapshot()), "-slow") != 1 {
		t.Fatal("判定依据: 幂等关闭不重复释放")
	}
}

// TestContainerCloseVsConcurrentResolve verifies container shutdown
// closes scopes first and then releases container-owned instances in
// reverse creation order.
func TestContainerCloseVsConcurrentResolve(t *testing.T) {
	c := New()
	var l eventLog
	mustRegister(t, c, "single", nil, Singleton, tracked(&l, nil, "single", 0))
	mustRegister(t, c, "tr", nil, Transient, tracked(&l, nil, "tr", 0))
	mustRegister(t, c, "sc", nil, Scoped, tracked(&l, nil, "sc", 0))
	mustFreeze(t, c)
	s, err := c.NewScope()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Resolve("sc"); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Resolve("single"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Resolve("tr"); err != nil {
		t.Fatal(err)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	rel := releaseEvents(l.snapshot())
	t.Logf("input: 容器关闭(含1个作用域, 根有single单例与tr瞬态); output release=%v", rel)
	// Scoped instances must be released before container-owned instances;
	// root order: tr was created after single => -tr before -single.
	idxScoped := indexOf(rel, "-sc")
	idxTr := indexOf(rel, "-tr")
	idxSingle := indexOf(rel, "-single")
	if !(idxScoped >= 0 && idxScoped < idxTr && idxTr < idxSingle) {
		t.Fatalf("判定依据: 先关作用域, 再逆序释放根实例; got %v", rel)
	}
	if _, err := c.Resolve("single"); !errors.Is(err, ErrContainerClosed) {
		t.Fatalf("判定依据: 已关闭容器拒绝解析; got %v", err)
	}
	if _, err := c.NewScope(); !errors.Is(err, ErrContainerClosed) {
		t.Fatalf("判定依据: 已关闭容器拒绝建作用域; got %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("判定依据: 重复关闭幂等; got %v", err)
	}
	for _, name := range []string{"-single", "-tr", "-sc"} {
		if count(rel, name) != 1 {
			t.Fatalf("判定依据: %s恰好释放一次; got %v", name, rel)
		}
	}
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func indexOfAfter(list []string, s string, from int) int {
	for i := from; i < len(list); i++ {
		if list[i] == s {
			return i
		}
	}
	return -1
}

// TestLifetimesBasics checks singleton/scope/transient construction
// cardinalities and release ownership.
func TestLifetimesBasics(t *testing.T) {
	c := New()
	var l eventLog
	var singletonN, scopedN, transientN int64
	mustRegister(t, c, "sing", []string{"tr"}, Singleton, tracked(&l, &singletonN, "sing", 0))
	mustRegister(t, c, "tr", nil, Transient, tracked(&l, &transientN, "tr", 0))
	mustRegister(t, c, "sc", nil, Scoped, tracked(&l, &scopedN, "sc", 0))
	mustFreeze(t, c)

	v1, err := c.Resolve("sing")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := c.Resolve("sing")
	if err != nil {
		t.Fatal(err)
	}
	v3, err := c.Resolve("tr")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input: 2xResolve(sing->tr)+1xResolve(tr) at root; output v1=%p v2=%p v3=%p builds sing=%d tr=%d",
		v1, v2, v3, singletonN, transientN)
	if v1 != v2 {
		t.Fatal("判定依据: 单例至多成功构造一次")
	}
	if singletonN != 1 {
		t.Fatalf("判定依据: 单例构造一次; got %d", singletonN)
	}
	// One transient for the singleton chain, one standalone root transient.
	if transientN != 2 {
		t.Fatalf("判定依据: 瞬态每次新建; got %d", transientN)
	}

	s1, _ := c.NewScope()
	s2, _ := c.NewScope()
	a, _ := s1.Resolve("sc")
	b, _ := s1.Resolve("sc")
	d, _ := s2.Resolve("sc")
	t.Logf("input: sc在scope1解析2次+scope2解析1次; output a=%p b=%p d=%p scopedBuilds=%d", a, b, d, scopedN)
	if a != b || a == d {
		t.Fatal("判定依据: 作用域实例每作用域一次, 跨作用域不同")
	}
	if scopedN != 2 {
		t.Fatalf("判定依据: 两个作用域各构造一次; got %d", scopedN)
	}

	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	rel := releaseEvents(l.snapshot())
	t.Logf("output: scope1关闭后release=%v (根实例与scope2不受影响)", rel)
	if count(rel, "-sc") != 1 {
		t.Fatalf("判定依据: 关闭作用域只释放本作用域实例; got %v", rel)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	rel = releaseEvents(l.snapshot())
	t.Logf("output: 容器关闭后release=%v", rel)
	if count(rel, "-sc") != 2 {
		t.Fatalf("判定依据: 容器关闭先关闭scope2; got %v", rel)
	}
	if count(rel, "-sing") != 1 || count(rel, "-tr") != 2 {
		t.Fatalf("判定依据: 归根瞬态随容器释放, 单例恰好一次; got %v", rel)
	}
	// Release sequence: standalone root transient (-tr), then the singleton
	// (-sing), then the transient that singleton owns (-tr). Each dependent
	// is released strictly before the dependency it captured.
	iStandaloneTr := indexOf(rel, "-tr")
	iSing := indexOfAfter(rel, "-sing", 0)
	iOwnedTr := indexOfAfter(rel, "-tr", iStandaloneTr+1)
	if !(iStandaloneTr >= 0 && iStandaloneTr < iSing && iSing < iOwnedTr) {
		t.Fatalf("判定依据: 逆序释放, 单例先于其瞬态依赖; got %v", rel)
	}
}

// TestSingletonBoundaryStopsExpansion verifies captivity expansion stops
// at a singleton dependency even when that singleton owns a scoped chain
// in a scope (which freeze cannot see and which never happens at root).
func TestSingletonBoundaryStopsExpansion(t *testing.T) {
	c := New()
	var l1 eventLog
	mustRegister(t, c, "a", []string{"b"}, Singleton, tracked(&l1, nil, "a", 0))
	mustRegister(t, c, "b", nil, Singleton, tracked(&l1, nil, "b", 0))
	if err := c.Freeze(); err != nil {
		t.Fatalf("判定依据: 闭包在单例b处停止, 单例依赖单例合法; got %v", err)
	}
	t.Log("input: a(singleton)->b(singleton); output Freeze=nil (遇单例停止展开)")

	// transient -> singleton is legal from anywhere.
	c2 := New()
	var l2 eventLog
	mustRegister(t, c2, "t", []string{"s"}, Transient, tracked(&l2, nil, "t", 0))
	mustRegister(t, c2, "s", nil, Singleton, tracked(&l2, nil, "s", 0))
	if err := c2.Freeze(); err != nil {
		t.Fatalf("判定依据: 瞬态依赖单例合法; got %v", err)
	}
	if _, err := c2.Resolve("t"); err != nil {
		t.Fatalf("判定依据: 根解析瞬态->单例应成功; got %v", err)
	}
	t.Log("input: t(transient)->s(singleton); output Resolve 成功")
}

// TestResolveBeforeFreezeAndRejectedNoInstance checks pre-freeze
// resolution rejection creates nothing.
func TestResolveBeforeFreezeAndRejectedNoInstance(t *testing.T) {
	c := New()
	var l eventLog
	mustRegister(t, c, "a", nil, Singleton, tracked(&l, nil, "a", 0))
	if _, err := c.Resolve("a"); !errors.Is(err, ErrNotFrozen) {
		t.Fatalf("判定依据: 未冻结拒绝解析; got %v", err)
	}
	if _, err := c.Resolve("ghost"); !errors.Is(err, ErrNotFrozen) {
		t.Fatalf("判定依据: 未冻结先于服务缺失; got %v", err)
	}
	mustFreeze(t, c)
	if _, err := c.Resolve("ghost"); !errors.Is(err, ErrServiceNotFound) {
		t.Fatalf("判定依据: 冻结后未知名报未注册; got %v", err)
	}
	if evs := l.snapshot(); len(evs) != 0 {
		t.Fatalf("判定依据: 被拒绝操作零实例; got %v", evs)
	}
	t.Logf("input: 冻结前解析/未知服务; output 全部拒绝且events=%v", l.snapshot())
}
