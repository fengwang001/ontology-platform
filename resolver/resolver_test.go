package resolver

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock 是可手动推进的毫秒时钟。
type fakeClock struct {
	mu  sync.Mutex
	now int64
}

func (f *fakeClock) get() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) advance(d int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now += d
}

// scriptUpstream 按固定脚本应答并统计每个名字的查询次数。
type scriptUpstream struct {
	mu      sync.Mutex
	answers map[Name]Answer
	errs    map[Name]error
	calls   map[Name]int
	order   []Name
	block   map[Name]chan struct{}
	entered map[Name]chan struct{}
	started map[Name]bool
}

func newScriptUpstream() *scriptUpstream {
	return &scriptUpstream{
		answers: map[Name]Answer{},
		errs:    map[Name]error{},
		calls:   map[Name]int{},
		block:   map[Name]chan struct{}{},
		entered: map[Name]chan struct{}{},
		started: map[Name]bool{},
	}
}

func (s *scriptUpstream) set(name Name, ans Answer) *scriptUpstream {
	s.answers[name] = ans
	return s
}

func (s *scriptUpstream) fail(name Name, err error) *scriptUpstream {
	s.errs[name] = err
	return s
}

func (s *scriptUpstream) blockOn(name Name) (release, entered chan struct{}) {
	ch := make(chan struct{})
	entered = make(chan struct{})
	s.block[name] = ch
	s.entered[name] = entered
	return ch, entered
}

func (s *scriptUpstream) query(name Name) (Answer, error) {
	if ch := s.block[name]; ch != nil {
		s.mu.Lock()
		if e := s.entered[name]; e != nil && !s.started[name] {
			s.started[name] = true
			close(e)
		}
		s.mu.Unlock()
		<-ch
	}
	s.mu.Lock()
	s.calls[name]++
	s.order = append(s.order, name)
	ans, aok := s.answers[name]
	err, eok := s.errs[name]
	s.mu.Unlock()
	if eok {
		return Answer{}, err
	}
	if !aok {
		return Answer{}, fmt.Errorf("upstream: no record for %q", name)
	}
	return ans, nil
}

func (s *scriptUpstream) callCount(name Name) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[name]
}

type testLogger struct{ t *testing.T }

func (l *testLogger) Logf(format string, args ...any) { l.t.Logf(format, args...) }

type bufLogger struct{ sb strings.Builder }

func (b *bufLogger) Logf(format string, args ...any) {
	fmt.Fprintf(&b.sb, format+"\n", args...)
}

func (b *bufLogger) String() string { return b.sb.String() }

func newTestCache(t *testing.T, cap int, up Upstream, clk *fakeClock, logger Logger) *Cache {
	t.Helper()
	return New(Config{Capacity: cap, Upstream: up, Clock: clk.get, Logger: logger})
}

func testCacheLog(t *testing.T, cap int, up Upstream, clk *fakeClock) (*Cache, *bufLogger) {
	t.Helper()
	l := &bufLogger{}
	return New(Config{Capacity: cap, Upstream: up, Clock: clk.get, Logger: l}), l
}

// 链 a(ttl=10) -> b(ttl=5) -> 地址(ttl=20)，返回 TTL 必须是最小值 5。
func TestResolve_TTLIsMinRemainingAcrossChain(t *testing.T) {
	clk := &fakeClock{}
	up := newScriptUpstream().
		set("a", AliasAnswer("b", 10)).
		set("b", AliasAnswer("c", 5)).
		set("c", AddressAnswer([]Address{"1.1.1.1"}, 20))
	c := newTestCache(t, 10, up.query, clk, &testLogger{t})

	res, err := c.Resolve("a")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !res.Found || res.TTL != 5 {
		t.Fatalf("got found=%v ttl=%d, want found=true ttl=5", res.Found, res.TTL)
	}
	if len(res.Chain) != 2 ||
		res.Chain[0] != (AliasLink{"a", "b"}) ||
		res.Chain[1] != (AliasLink{"b", "c"}) {
		t.Fatalf("unexpected chain: %+v", res.Chain)
	}
	if fmt.Sprint(res.Addresses) != "[1.1.1.1]" {
		t.Fatalf("unexpected addresses: %v", res.Addresses)
	}

	// 第二次解析所有环命中缓存，上游各名字只查一次。
	clk.advance(1)
	if _, err := c.Resolve("a"); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	for _, n := range []Name{"a", "b", "c"} {
		if got := up.callCount(n); got != 1 {
			t.Fatalf("upstream calls for %q = %d, want 1", n, got)
		}
	}
}

// 别名环命中缓存而地址环已失效：只补查地址环这一个缺失环节。
func TestResolve_AliasHitsOnlyRefetchMissingAddressHop(t *testing.T) {
	clk := &fakeClock{}
	up := newScriptUpstream().
		set("a", AliasAnswer("b", 100)).
		set("b", AliasAnswer("c", 100)).
		set("c", AddressAnswer([]Address{"2.2.2.2"}, 4))
	c := newTestCache(t, 10, up.query, clk, &testLogger{t})

	if _, err := c.Resolve("a"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	clk.advance(5) // 仅 c 到期
	up.set("c", AddressAnswer([]Address{"9.9.9.9"}, 50))
	res, err := c.Resolve("a")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if fmt.Sprint(res.Addresses) != "[9.9.9.9]" {
		t.Fatalf("addresses = %v, want [9.9.9.9]", res.Addresses)
	}
	// 剩余：a=95, b=95；地址环新得 50，最小值为 50。
	if res.TTL != 50 {
		t.Fatalf("ttl = %d, want 50", res.TTL)
	}
	if up.callCount("a") != 1 || up.callCount("b") != 1 {
		t.Fatalf("alias hops re-queried: a=%d b=%d", up.callCount("a"), up.callCount("b"))
	}
	if up.callCount("c") != 2 {
		t.Fatalf("address hop calls = %d, want 2", up.callCount("c"))
	}
}

// 否定条目的存活时间取 min(S, M)。
func TestResolve_NegativeTTLIsMinOfSOAAndMinimum(t *testing.T) {
	clk := &fakeClock{}
	up := newScriptUpstream().
		set("a", AliasAnswer("b", 100)).
		set("b", NXDOMAINAnswer(30, 7)).
		set("d", NXDOMAINAnswer(3, 99))
	c, logs := testCacheLog(t, 10, up.query, clk)

	res, err := c.Resolve("a")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Found || res.TTL != 7 {
		t.Fatalf("got found=%v ttl=%d, want false/7", res.Found, res.TTL)
	}
	res2, err := c.Resolve("d")
	if err != nil {
		t.Fatalf("resolve d: %v", err)
	}
	if res2.Found || res2.TTL != 3 {
		t.Fatalf("got found=%v ttl=%d, want false/3", res2.Found, res2.TTL)
	}
	if !strings.Contains(logs.String(), "min ttl=7") {
		t.Fatalf("log missing min-ttl rationale:\n%s", logs.String())
	}
}

// 存活时间为 0 的记录参与本次解析但不写入缓存。
func TestResolve_ZeroTTLNotCachedButUsed(t *testing.T) {
	clk := &fakeClock{}
	up := newScriptUpstream().
		set("a", AliasAnswer("b", 0)).
		set("b", AddressAnswer([]Address{"3.3.3.3"}, 10))
	c := newTestCache(t, 10, up.query, clk, &testLogger{t})

	res, err := c.Resolve("a")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !res.Found || res.TTL != 0 {
		t.Fatalf("got found=%v ttl=%d, want true/0", res.Found, res.TTL)
	}
	if c.Len() != 1 {
		t.Fatalf("cache len = %d, want 1 (only b stored)", c.Len())
	}
	if _, err := c.Resolve("a"); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if up.callCount("a") != 2 {
		t.Fatalf("upstream calls for zero-ttl a = %d, want 2", up.callCount("a"))
	}
	if up.callCount("b") != 1 {
		t.Fatalf("upstream calls for b = %d, want 1", up.callCount("b"))
	}
}

// 恰在到期时刻（now == expireAt）即失效。
func TestResolve_ExpiresExactlyAtDeadline(t *testing.T) {
	clk := &fakeClock{}
	up := newScriptUpstream().
		set("a", AddressAnswer([]Address{"4.4.4.4"}, 10))
	c := newTestCache(t, 10, up.query, clk, &testLogger{t})

	if _, err := c.Resolve("a"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	clk.advance(9)
	if _, err := c.Resolve("a"); err != nil {
		t.Fatalf("resolve at t=9: %v", err)
	}
	if up.callCount("a") != 1 {
		t.Fatalf("calls before deadline = %d, want 1", up.callCount("a"))
	}

	clk.advance(1) // 现在恰为到期时刻
	if _, err := c.Resolve("a"); err != nil {
		t.Fatalf("resolve at t=10: %v", err)
	}
	if up.callCount("a") != 2 {
		t.Fatalf("calls at deadline = %d, want 2 (expired)", up.callCount("a"))
	}
}

// 成环失败后，已取得的部分记录不得写入，缓存保持调用前状态。
func TestResolve_CycleLeavesCacheUntouched(t *testing.T) {
	clk := &fakeClock{}
	up := newScriptUpstream().
		set("a", AliasAnswer("b", 10)).
		set("b", AliasAnswer("c", 10)).
		set("c", AliasAnswer("a", 10))
	c := newTestCache(t, 2, up.query, clk, &testLogger{t})

	if _, err := c.Resolve("a"); !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
	if c.Len() != 0 {
		t.Fatalf("cache len after cycle = %d, want 0", c.Len())
	}

	// 缓存与上游混合成环：先让 seed->a->b 入缓存，再让 b 指回已访问的 seed。
	up2 := newScriptUpstream().
		set("seed", AliasAnswer("a", 100)).
		set("a", AliasAnswer("b", 100)).
		set("b", AliasAnswer("seed", 100))
	c2 := newTestCache(t, 10, up2.query, &fakeClock{}, &testLogger{t})
	if _, err := c2.Resolve("seed"); !errors.Is(err, ErrCycle) {
		t.Fatalf("mixed cache/upstream cycle err = %v, want ErrCycle", err)
	}
	if c2.Len() != 0 {
		t.Fatalf("cache len after mixed cycle = %d, want 0", c2.Len())
	}
}

// 链过长：8 条别名 + 终点允许；第 9 条别名被拒绝且不污染缓存。
func TestResolve_ChainTooLong(t *testing.T) {
	makeChain := func(length int, terminal Answer) *scriptUpstream {
		up := newScriptUpstream()
		for i := 0; i < length; i++ {
			up.set(fmt.Sprintf("n%d", i), AliasAnswer(fmt.Sprintf("n%d", i+1), 100))
		}
		up.set(fmt.Sprintf("n%d", length), terminal)
		return up
	}

	c8 := newTestCache(t, 20, makeChain(8, AddressAnswer([]Address{"8.8.8.8"}, 100)).query,
		&fakeClock{}, &testLogger{t})
	res, err := c8.Resolve("n0")
	if err != nil {
		t.Fatalf("8 aliases + terminal should succeed: %v", err)
	}
	if len(res.Chain) != 8 {
		t.Fatalf("chain len = %d, want 8", len(res.Chain))
	}

	c9 := newTestCache(t, 20, makeChain(9, AddressAnswer([]Address{"9.9.9.9"}, 100)).query,
		&fakeClock{}, &testLogger{t})
	if _, err := c9.Resolve("n0"); !errors.Is(err, ErrChainTooLong) {
		t.Fatalf("err = %v, want ErrChainTooLong", err)
	}
	if c9.Len() != 0 {
		t.Fatalf("cache len = %d, want 0 after rejection", c9.Len())
	}
}

// 上游失败不污染缓存；失败后重试可成功。
func TestResolve_UpstreamFailureDoesNotPolluteCache(t *testing.T) {
	clk := &fakeClock{}
	upstreamErr := errors.New("upstream unavailable")
	up := newScriptUpstream().
		set("a", AliasAnswer("b", 10)).
		fail("b", upstreamErr)
	c := newTestCache(t, 10, up.query, clk, &testLogger{t})

	_, err := c.Resolve("a")
	if !errors.Is(err, upstreamErr) {
		t.Fatalf("err = %v, want upstream error", err)
	}
	if c.Len() != 0 {
		t.Fatalf("cache len = %d, want 0 after upstream failure", c.Len())
	}

	delete(up.errs, "b")
	up.set("b", AddressAnswer([]Address{"5.5.5.5"}, 10))
	res, err := c.Resolve("a")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !res.Found || fmt.Sprint(res.Addresses) != "[5.5.5.5]" {
		t.Fatalf("retry result = %+v", res)
	}
	if up.callCount("a") != 2 {
		t.Fatalf("a should be re-queried after failed attempt, calls=%d", up.callCount("a"))
	}
}

// 拒绝顺序：空名最先（不查上游）。
func TestResolve_EmptyNameRejectedFirst(t *testing.T) {
	clk := &fakeClock{}
	up := newScriptUpstream()
	c, logs := testCacheLog(t, 10, up.query, clk)

	_, err := c.Resolve("")
	if !errors.Is(err, ErrEmptyName) {
		t.Fatalf("err = %v, want ErrEmptyName", err)
	}
	if c.Len() != 0 {
		t.Fatalf("cache len = %d, want 0", c.Len())
	}
	if len(up.calls) != 0 {
		t.Fatalf("upstream must not be called for empty name, calls=%v", up.calls)
	}
	if !strings.Contains(logs.String(), "empty name (checked first)") {
		t.Fatalf("log should state rejection rationale:\n%s", logs.String())
	}
}

// 并发解析同一未命中名字：上游只查询一次，所有等待者拿到同一结果。
func TestResolve_ConcurrentSameNameSingleUpstreamCall(t *testing.T) {
	clk := &fakeClock{}
	up := newScriptUpstream().
		set("a", AliasAnswer("b", 100)).
		set("b", AddressAnswer([]Address{"6.6.6.6"}, 100))
	releaseA, enteredA := up.blockOn("a")
	c := newTestCache(t, 10, up.query, clk, nil)

	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		c.Resolve("a")
	}()
	<-enteredA // 首个解析已经进入上游查询并持有合并锁

	const n = 16
	var wg sync.WaitGroup
	results := make([]*Result, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = c.Resolve("a")
		}(i)
	}
	time.Sleep(20 * time.Millisecond) // 让等待者全部挂到共享调用上
	close(releaseA)
	wg.Wait()
	<-leaderDone

	if up.callCount("a") != 1 {
		t.Fatalf("upstream calls for a = %d, want 1", up.callCount("a"))
	}
	if up.callCount("b") != 1 {
		t.Fatalf("upstream calls for b = %d, want 1", up.callCount("b"))
	}
	want := results[0]
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("waiter %d err = %v", i, errs[i])
		}
		if results[i] != want {
			t.Fatalf("waiter %d got different result instance; shared resolution expected", i)
		}
	}
}

// 并发同一名字且上游失败：只查询一次，所有等待者得到同一错误。
func TestResolve_ConcurrentSameNameSharedFailure(t *testing.T) {
	clk := &fakeClock{}
	upstreamErr := errors.New("boom")
	up := newScriptUpstream().fail("a", upstreamErr)
	release, entered := up.blockOn("a")
	c := newTestCache(t, 10, up.query, clk, nil)

	leaderDone := make(chan struct{})
	go func() {
		defer close(leaderDone)
		c.Resolve("a")
	}()
	<-entered

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.Resolve("a")
		}(i)
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	<-leaderDone

	if up.callCount("a") != 1 {
		t.Fatalf("upstream calls = %d, want 1", up.callCount("a"))
	}
	for i := 0; i < n; i++ {
		if !errors.Is(errs[i], upstreamErr) {
			t.Fatalf("waiter %d err = %v", i, errs[i])
		}
	}
	if c.Len() != 0 {
		t.Fatalf("cache len = %d, want 0", c.Len())
	}
}

// 并发不同名字时，共同的缺失环节上游也只查一次，且缓存不超过容量。
func TestResolve_ConcurrentSharedHopAndCapacity(t *testing.T) {
	clk := &fakeClock{}
	up := newScriptUpstream().
		set("a1", AliasAnswer("shared", 100)).
		set("a2", AliasAnswer("shared", 100)).
		set("a3", AliasAnswer("shared", 100)).
		set("shared", AddressAnswer([]Address{"7.7.7.7"}, 100))
	release, entered := up.blockOn("shared")
	c := newTestCache(t, 3, up.query, clk, nil)

	var wg sync.WaitGroup
	for _, n := range []Name{"a1", "a2", "a3"} {
		wg.Add(1)
		go func(name Name) {
			defer wg.Done()
			res, err := c.Resolve(name)
			if err != nil || !res.Found || res.TTL != 100 {
				t.Errorf("resolve %q = %+v, %v", name, res, err)
			}
		}(n)
	}
	<-entered
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if up.callCount("shared") != 1 {
		t.Fatalf("upstream calls for shared hop = %d, want 1", up.callCount("shared"))
	}
	if c.Len() > 3 {
		t.Fatalf("cache len = %d, must not exceed capacity 3", c.Len())
	}
}

// 淘汰规则：先清失效，仍满则淘汰到期最早者；并列取名字字典序小者。
func TestCache_EarliestExpiryEvictedWithLexicographicTieBreak(t *testing.T) {
	clk := &fakeClock{}
	up := newScriptUpstream().
		set("zeta", AddressAnswer([]Address{"z"}, 100)).
		set("mid", AddressAnswer([]Address{"m"}, 5)).
		set("alpha", AddressAnswer([]Address{"a"}, 100)).
		set("new", AddressAnswer([]Address{"n"}, 100))
	c := newTestCache(t, 3, up.query, clk, nil)

	for _, n := range []Name{"zeta", "mid", "alpha"} {
		if _, err := c.Resolve(n); err != nil {
			t.Fatalf("seed %q: %v", n, err)
		}
	}
	// 容量已满，存入 new 时淘汰到期最早的 mid。
	if _, err := c.Resolve("new"); err != nil {
		t.Fatalf("resolve new: %v", err)
	}
	c.mu.Lock()
	_, midGone := c.store["mid"]
	_, zetaAlive := c.store["zeta"]
	_, alphaAlive := c.store["alpha"]
	_, newAlive := c.store["new"]
	c.mu.Unlock()
	if midGone || !zetaAlive || !alphaAlive || !newAlive {
		t.Fatalf("eviction wrong: midGone=%v zeta=%v alpha=%v new=%v",
			midGone, zetaAlive, alphaAlive, newAlive)
	}

	// 并列到期：清失效后仍满，淘汰字典序最小的 alpha。
	clk2 := &fakeClock{}
	up2 := newScriptUpstream().
		set("zeta", AddressAnswer([]Address{"z"}, 10)).
		set("alpha", AddressAnswer([]Address{"a"}, 10)).
		set("beta", AddressAnswer([]Address{"b"}, 10)).
		set("late", AddressAnswer([]Address{"l"}, 100))
	c2 := newTestCache(t, 3, up2.query, clk2, nil)
	for _, n := range []Name{"zeta", "alpha", "beta"} {
		if _, err := c2.Resolve(n); err != nil {
			t.Fatalf("seed %q: %v", n, err)
		}
	}
	clk2.advance(20) // 三条全部失效；存入 late 前先清除，无需淘汰。
	if _, err := c2.Resolve("late"); err != nil {
		t.Fatalf("resolve late: %v", err)
	}
	c2.mu.Lock()
	n := len(c2.store)
	_, onlyLate := c2.store["late"]
	c2.mu.Unlock()
	if n != 1 || !onlyLate {
		t.Fatalf("expired entries must be purged first, len=%d onlyLate=%v", n, onlyLate)
	}

	// 真正并列且都存活：alpha 与 zeta 同到期时刻，淘汰 alpha。
	clk3 := &fakeClock{}
	up3 := newScriptUpstream().
		set("zeta", AddressAnswer([]Address{"z"}, 10)).
		set("alpha", AddressAnswer([]Address{"a"}, 10)).
		set("incoming", AddressAnswer([]Address{"i"}, 10))
	c3 := newTestCache(t, 2, up3.query, clk3, nil)
	for _, n := range []Name{"zeta", "alpha"} {
		if _, err := c3.Resolve(n); err != nil {
			t.Fatalf("seed %q: %v", n, err)
		}
	}
	if _, err := c3.Resolve("incoming"); err != nil {
		t.Fatalf("resolve incoming: %v", err)
	}
	c3.mu.Lock()
	_, alphaGone := c3.store["alpha"]
	_, zetaStill := c3.store["zeta"]
	_, incomingAlive := c3.store["incoming"]
	c3.mu.Unlock()
	if alphaGone || !zetaStill || !incomingAlive {
		t.Fatalf("tie-break wrong: alphaGone=%v zeta=%v incoming=%v",
			alphaGone, zetaStill, incomingAlive)
	}
}

// 相同调用序列在两个独立缓存上重放，结果与存活时间完全相同。
func TestResolve_DeterministicReplay(t *testing.T) {
	script := func() *scriptUpstream {
		return newScriptUpstream().
			set("a", AliasAnswer("b", 10)).
			set("b", AliasAnswer("c", 3)).
			set("c", AddressAnswer([]Address{"10.0.0.1"}, 8))
	}
	run := func() []*Result {
		clk := &fakeClock{}
		c := newTestCache(t, 10, script().query, clk, nil)
		var out []*Result
		r1, err := c.Resolve("a")
		if err != nil {
			t.Fatal(err)
		}
		clk.advance(2)
		r2, err := c.Resolve("a")
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r1, r2)
		return out
	}
	first := run()
	second := run()
	for i := range first {
		if fmt.Sprintf("%+v", first[i]) != fmt.Sprintf("%+v", second[i]) {
			t.Fatalf("replay mismatch at %d:\n%+v\nvs\n%+v", i, first[i], second[i])
		}
	}
	// 第二次调用时 b 剩余最少：ttl 应为 3-2=1。
	if first[1].TTL != 1 {
		t.Fatalf("second call ttl = %d, want 1", first[1].TTL)
	}
}
