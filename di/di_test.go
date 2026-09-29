package di

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// rec 是可释放的测试实例，保存依赖以便断言菱形共享。
type rec struct {
	name string
	deps map[string]*rec
	log  *orderLog
}

func (r *rec) Dispose() { r.log.disposed(r.name) }

type orderLog struct {
	mu        sync.Mutex
	construct []string
	dispose   []string
}

func (l *orderLog) constructed(name string) {
	l.mu.Lock()
	l.construct = append(l.construct, name)
	l.mu.Unlock()
}

func (l *orderLog) disposed(name string) {
	l.mu.Lock()
	l.dispose = append(l.dispose, name)
	l.mu.Unlock()
}

func (l *orderLog) snapshot() ([]string, []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.construct...), append([]string(nil), l.dispose...)
}

type ctl struct {
	log    *orderLog
	count  int32
	failAt int32
	err    error
}

func (ct *ctl) ctor(name string) Constructor {
	return func(deps map[string]any) (any, error) {
		n := atomic.AddInt32(&ct.count, 1)
		if ct.failAt > 0 && n == ct.failAt {
			return nil, ct.err
		}
		ct.log.constructed(name)
		rd := make(map[string]*rec, len(deps))
		for k, v := range deps {
			rd[k] = v.(*rec)
		}
		return &rec{name: name, deps: rd, log: ct.log}, nil
	}
}

func mustFreeze(t *testing.T, c *Container) {
	t.Helper()
	if err := c.Freeze(); err != nil {
		t.Fatalf("freeze: %v", err)
	}
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

func TestLifetimesAndCaching(t *testing.T) {
	log := &orderLog{}
	sCtl := &ctl{log: log}
	scCtl := &ctl{log: log}
	tCtl := &ctl{log: log}
	c := New()
	mustReg(t, c, Registration{Name: "s", Lifetime: Singleton, Construct: sCtl.ctor("s")})
	mustReg(t, c, Registration{Name: "sc", Lifetime: Scoped, Construct: scCtl.ctor("sc")})
	mustReg(t, c, Registration{Name: "t", Lifetime: Transient, Dependencies: []string{"s", "sc"},
		Construct: tCtl.ctor("t")})
	mustFreeze(t, c)

	sc1, err := c.NewScope("sc1")
	if err != nil {
		t.Fatal(err)
	}
	sc2, err := c.NewScope("sc2")
	if err != nil {
		t.Fatal(err)
	}

	v1, err := sc1.Resolve("s")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := sc2.Resolve("s")
	if err != nil || v1 != v2 {
		t.Fatalf("singleton not shared across scopes: %v %v", v1, v2)
	}
	if got := atomic.LoadInt32(&sCtl.count); got != 1 {
		t.Fatalf("singleton constructed %d times, want 1", got)
	}

	a1, err := sc1.Resolve("t")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := sc1.Resolve("t")
	if err != nil {
		t.Fatal(err)
	}
	if a1 == a2 {
		t.Fatal("transient must be new per resolve")
	}
	b1, err := sc2.Resolve("t")
	if err != nil {
		t.Fatal(err)
	}
	r1, r2, rb := a1.(*rec), a2.(*rec), b1.(*rec)
	if r1.deps["sc"] != r2.deps["sc"] {
		t.Fatal("diamond: scoped instance not shared within scope")
	}
	if r1.deps["sc"] == rb.deps["sc"] {
		t.Fatal("scoped instance leaked across scopes")
	}
	if r1.deps["s"] != r2.deps["s"] || r1.deps["s"] != rb.deps["s"] {
		t.Fatal("singleton dependency must be the same instance everywhere")
	}
	if got := atomic.LoadInt32(&scCtl.count); got != 2 {
		t.Fatalf("scoped constructed %d times, want 2 (one per scope)", got)
	}

	before := atomic.LoadInt32(&scCtl.count)
	if _, err := c.Resolve("sc"); !errors.Is(err, ErrScopedFromRoot) {
		t.Fatalf("want ErrScopedFromRoot, got %v", err)
	}
	if atomic.LoadInt32(&scCtl.count) != before {
		t.Fatal("rejected resolve created an instance")
	}

	cons, dis := log.snapshot()
	t.Logf("input lifetimes/sharing -> output construct=%v dispose=%v | 判定依据: singleton x1, scoped 每作用域 x1, transient 每次新建, 根解析 scoped 被拒", cons, dis)
}

func TestConstructFailureRollsBackInReverse(t *testing.T) {
	// 三层图，第三层有兄弟构造失败：
	//   a(singleton) -> t1(transient)，t1 依赖 g1、g2（成功）与 bad（第三层失败）
	// t1 自身构造函数因 bad 失败不会执行；已成功的 g1、g2 按创建逆序释放；
	// a 未构造成功不释放；单例未缓存，修复后重试重新构造。
	log := &orderLog{}
	aCtl := &ctl{log: log}
	t1Ctl := &ctl{log: log}
	g1Ctl := &ctl{log: log}
	g2Ctl := &ctl{log: log}
	badCtl := &ctl{log: log, failAt: 1, err: errors.New("boom")}

	c := New()
	mustReg(t, c, Registration{Name: "a", Lifetime: Singleton, Dependencies: []string{"t1"}, Construct: aCtl.ctor("a")})
	mustReg(t, c, Registration{Name: "t1", Lifetime: Transient, Dependencies: []string{"g1", "g2", "bad"}, Construct: t1Ctl.ctor("t1")})
	mustReg(t, c, Registration{Name: "g1", Lifetime: Transient, Construct: g1Ctl.ctor("g1")})
	mustReg(t, c, Registration{Name: "g2", Lifetime: Transient, Construct: g2Ctl.ctor("g2")})
	mustReg(t, c, Registration{Name: "bad", Lifetime: Transient, Construct: badCtl.ctor("bad")})
	mustFreeze(t, c)

	if _, err := c.Resolve("a"); err == nil {
		t.Fatal("expected construction error, got nil")
	}

	cons, dis := log.snapshot()
	t.Logf("input third-level failure -> construct=%v dispose=%v", cons, dis)
	if len(dis) != 2 || dis[0] != "g2" || dis[1] != "g1" {
		t.Fatalf("expected reverse dispose [g2 g1], got %v", dis)
	}
	for _, n := range cons {
		if n == "t1" || n == "a" {
			t.Fatalf("%q constructor must not run after dependency failure: %v", n, cons)
		}
	}
	for _, n := range cons {
		if n == "a" {
			t.Fatal("singleton constructor must not run after dependency failure")
		}
	}

	atomic.StoreInt32(&badCtl.failAt, 0)
	v, err := c.Resolve("a")
	if err != nil {
		t.Fatalf("retry resolve: %v", err)
	}
	v2, err := c.Resolve("a")
	if err != nil || v != v2 {
		t.Fatal("retried singleton was not cached")
	}
	if got := atomic.LoadInt32(&g2Ctl.count); got != 2 {
		t.Fatalf("g2 constructed %d times, want 2 (failed attempt + retry)", got)
	}
	t.Logf("input retry after fix -> output singleton cached | 判定依据: 失败不缓存, 重试重新构造")
}

func TestSuccessfulSingletonSurvivesSiblingFailure(t *testing.T) {
	// root(transient) 依赖 s1(singleton 成功) 与 bad(singleton 失败)。
	// s1 已构造成功必须保留且只构造一次；root 随失败回滚。
	log := &orderLog{}
	s1Ctl := &ctl{log: log}
	badCtl := &ctl{log: log, failAt: 1, err: errors.New("bang")}
	rootCtl := &ctl{log: log}
	c := New()
	mustReg(t, c, Registration{Name: "root", Lifetime: Transient, Dependencies: []string{"s1", "bad"}, Construct: rootCtl.ctor("root")})
	mustReg(t, c, Registration{Name: "s1", Lifetime: Singleton, Construct: s1Ctl.ctor("s1")})
	mustReg(t, c, Registration{Name: "bad", Lifetime: Singleton, Construct: badCtl.ctor("bad")})
	mustFreeze(t, c)

	if _, err := c.Resolve("root"); err == nil {
		t.Fatal("expected failure")
	}
	_, dis := log.snapshot()
	for _, n := range dis {
		if n == "s1" {
			t.Fatal("successful singleton s1 must be retained")
		}
	}
	atomic.StoreInt32(&badCtl.failAt, 0)
	if _, err := c.Resolve("root"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := atomic.LoadInt32(&s1Ctl.count); got != 1 {
		t.Fatalf("s1 constructed %d times, want 1", got)
	}
	t.Logf("input sibling singleton failure -> dispose=%v | 判定依据: 已成功单例保留, 下次不重建", dis)
}

func TestDisposeOrderOnClose(t *testing.T) {
	log := &orderLog{}
	sCtl := &ctl{log: log}
	scCtl := &ctl{log: log}
	tCtl := &ctl{log: log}
	c := New()
	mustReg(t, c, Registration{Name: "s", Lifetime: Singleton, Dependencies: []string{"t"}, Construct: sCtl.ctor("s")})
	mustReg(t, c, Registration{Name: "t", Lifetime: Transient, Construct: tCtl.ctor("t")})
	mustReg(t, c, Registration{Name: "sc", Lifetime: Scoped, Dependencies: []string{"s"}, Construct: scCtl.ctor("sc")})
	mustFreeze(t, c)

	sc1, _ := c.NewScope("sc1")
	sc2, _ := c.NewScope("sc2")
	if _, err := sc1.Resolve("sc"); err != nil {
		t.Fatal(err)
	}
	if _, err := sc2.Resolve("sc"); err != nil {
		t.Fatal(err)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	_, dis := log.snapshot()
	t.Logf("input close with two scopes -> dispose=%v | 判定依据: 先作用域后单例, 依赖者先于依赖, 每个恰好一次", dis)

	lastSc, sIdx, tIdx := -1, -1, -1
	for i, n := range dis {
		if n == "sc" {
			lastSc = i
		}
		if n == "s" && sIdx < 0 {
			sIdx = i
		}
		if n == "t" && tIdx < 0 {
			tIdx = i
		}
	}
	if lastSc < 0 || sIdx < 0 || lastSc > sIdx {
		t.Fatalf("all scoped instances must dispose before singleton: %v", dis)
	}
	if tIdx < sIdx {
		t.Fatalf("transient dependency t must dispose after singleton s: %v", dis)
	}
	counts := map[string]int{}
	for _, n := range dis {
		counts[n]++
	}
	for name, want := range map[string]int{"sc": 2, "s": 1, "t": 1} {
		if counts[name] != want {
			t.Fatalf("%s disposed %d times, want %d (full: %v)", name, counts[name], want, dis)
		}
	}

	if err := c.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	if _, err := c.Resolve("s"); !errors.Is(err, ErrContainerClosed) {
		t.Fatalf("want ErrContainerClosed, got %v", err)
	}
	if _, err := sc1.Resolve("sc"); !errors.Is(err, ErrContainerClosed) {
		t.Fatalf("want closed error on child scope, got %v", err)
	}
}

func TestScopeCloseRejectsLaterResolve(t *testing.T) {
	log := &orderLog{}
	scCtl := &ctl{log: log}
	c := New()
	mustReg(t, c, Registration{Name: "sc", Lifetime: Scoped, Construct: scCtl.ctor("sc")})
	mustFreeze(t, c)
	sc, _ := c.NewScope("s")
	if _, err := sc.Resolve("sc"); err != nil {
		t.Fatal(err)
	}
	if err := sc.Close(); err != nil {
		t.Fatal(err)
	}
	before := atomic.LoadInt32(&scCtl.count)
	if _, err := sc.Resolve("sc"); !errors.Is(err, ErrScopeClosed) && !errors.Is(err, ErrContainerClosed) {
		t.Fatalf("want closed error, got %v", err)
	}
	if atomic.LoadInt32(&scCtl.count) != before {
		t.Fatal("resolve after close created an instance")
	}
	if err := sc.Close(); err != nil {
		t.Fatalf("idempotent scope close: %v", err)
	}
	_, dis := log.snapshot()
	if len(dis) != 1 || dis[0] != "sc" {
		t.Fatalf("sc disposed %v, want exactly once", dis)
	}
	t.Logf("input scope close then resolve -> output closed error, dispose=%v | 判定依据: 关闭后解析拒绝且不建实例, 幂等", dis)
}
