package aliascache

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- 测试夹具 ----

type scriptedUpstream struct {
	mu      sync.Mutex
	answers map[string]UpstreamAnswer
	errs    map[string]error
	calls   []string
	started chan<- string
	release map[string]chan struct{}
}

func newScripted(answers map[string]UpstreamAnswer) *scriptedUpstream {
	return &scriptedUpstream{
		answers: answers,
		errs:    map[string]error{},
		release: map[string]chan struct{}{},
	}
}

func (s *scriptedUpstream) query(name string) (UpstreamAnswer, error) {
	s.mu.Lock()
	s.calls = append(s.calls, name)
	if s.started != nil {
		s.started <- name
	}
	rel := s.release[name]
	err := s.errs[name]
	ans, ok := s.answers[name]
	s.mu.Unlock()
	if rel != nil {
		<-rel
	}
	if err != nil {
		return UpstreamAnswer{}, err
	}
	if !ok {
		return UpstreamAnswer{}, errors.New("upstream: no answer for " + name)
	}
	return ans, nil
}

func (s *scriptedUpstream) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

type manualClock struct {
	mu sync.Mutex
	t  time.Time
}

func newManualClock() *manualClock {
	return &manualClock{t: time.Unix(1_000_000, 0)}
}

func (m *manualClock) now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.t
}

func (m *manualClock) advance(d time.Duration) {
	m.mu.Lock()
	m.t = m.t.Add(d)
	m.mu.Unlock()
}

func aliasAnswer(target string, ttl time.Duration) UpstreamAnswer {
	return UpstreamAnswer{Alias: &AliasRecord{Target: target, TTL: ttl}}
}

func addrAnswer(addrs []string, ttl time.Duration) UpstreamAnswer {
	return UpstreamAnswer{Addresses: &AddressRecord{Addresses: addrs, TTL: ttl}}
}

func negAnswer(s, m time.Duration) UpstreamAnswer {
	return UpstreamAnswer{Negative: &NegativeRecord{SOATTL: s, MinTTL: m}}
}

func newTestCache(t *testing.T, capacity int, up UpstreamFunc, clock *manualClock) (*Cache, *bytes.Buffer) {
	t.Helper()
	var log bytes.Buffer
	c := New(capacity, up, WithClock(clock.now), WithLogger(&log))
	return c, &log
}

// snapshot 返回当前缓存（名字 -> 条目种类/到期时刻），用于“缓存不变”断言。
func snapshotCache(c *Cache) map[string]entry {
	out := map[string]entry{}
	c.mu.Lock()
	for name, e := range c.entries {
		out[name] = *e
	}
	c.mu.Unlock()
	return out
}

func sameSnapshot(a, b map[string]entry) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok || w.kind != v.kind || w.alias != v.alias || w.expiresAt != v.expiresAt ||
			strings.Join(w.addresses, ",") != strings.Join(v.addresses, ",") {
			return false
		}
	}
	return true
}

func resultEqual(a, b Result) bool {
	return a.Name == b.Name && a.Found == b.Found && a.TTL == b.TTL &&
		strings.Join(a.Aliases, ",") == strings.Join(b.Aliases, ",") &&
		strings.Join(a.Addresses, ",") == strings.Join(b.Addresses, ",")
}

// 返回存活时间取链上所有环节剩余时间的最小值。
func TestTTLIsMinAcrossChain(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		"a.example": aliasAnswer("b.example", 10*time.Second),
		"b.example": aliasAnswer("c.example", 30*time.Second),
		"c.example": addrAnswer([]string{"1.1.1.1"}, 20*time.Second),
	})
	c, _ := newTestCache(t, 10, up.query, clock)

	res, err := c.Resolve("a.example")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !res.Found || res.TTL != 10*time.Second {
		t.Fatalf("got found=%v ttl=%v, want found=true ttl=10s", res.Found, res.TTL)
	}
	if strings.Join(res.Aliases, ",") != "b.example,c.example" {
		t.Fatalf("aliases = %v", res.Aliases)
	}
	if strings.Join(res.Addresses, ",") != "1.1.1.1" {
		t.Fatalf("addresses = %v", res.Addresses)
	}

	// 前进 6s：环 a 剩余 4s（最小），验证再次解析的剩余时间仍取链上最小值。
	clock.advance(6 * time.Second)
	res, err = c.Resolve("a.example")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.TTL != 4*time.Second {
		t.Fatalf("got ttl=%v, want 4s", res.TTL)
	}
	if up.callCount() != 3 {
		t.Fatalf("upstream calls = %d, want 3 (fully cached second time)", up.callCount())
	}
}

// 别名命中缓存而地址环已失效时，只补查地址环。
func TestOnlyRefetchExpiredTerminalHop(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		"a.example": aliasAnswer("b.example", 100*time.Second),
		"b.example": addrAnswer([]string{"2.2.2.2"}, 5*time.Second),
	})
	c, _ := newTestCache(t, 10, up.query, clock)

	if _, err := c.Resolve("a.example"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if up.callCount() != 2 {
		t.Fatalf("initial calls = %d, want 2", up.callCount())
	}

	// 地址环恰好到期（now == expiresAt）即失效；别名环仍存活。
	clock.advance(5 * time.Second)
	up.answers["b.example"] = addrAnswer([]string{"3.3.3.3"}, 50*time.Second)

	res, err := c.Resolve("a.example")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if strings.Join(res.Addresses, ",") != "3.3.3.3" {
		t.Fatalf("addresses = %v, want refreshed 3.3.3.3", res.Addresses)
	}
	if got := up.calls; len(got) != 3 || got[2] != "b.example" {
		t.Fatalf("calls = %v, want only one extra query for b.example", got)
	}
	if res.TTL != 50*time.Second {
		t.Fatalf("ttl = %v, want 50s", res.TTL)
	}
}

// 否定条目以 min(S, M) 存活，终链 TTL 同样取链上最小值。
func TestNegativeTTLIsMinOfSAndM(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		"a.example":       aliasAnswer("missing.example", 100*time.Second),
		"missing.example": negAnswer(60*time.Second, 15*time.Second),
	})
	c, _ := newTestCache(t, 10, up.query, clock)

	res, err := c.Resolve("a.example")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Found {
		t.Fatal("want not-found result")
	}
	if res.TTL != 15*time.Second {
		t.Fatalf("ttl = %v, want min(100s,15s)=15s", res.TTL)
	}

	// 否定结果应被缓存：第二次解析不再访问上游。
	_, _ = c.Resolve("a.example")
	if up.callCount() != 2 {
		t.Fatalf("calls = %d, negative result must be cached", up.callCount())
	}

	// 到期后 S < M 时取 S。
	clock.advance(20 * time.Second)
	up.answers["missing.example"] = negAnswer(7*time.Second, 30*time.Second)
	res, err = c.Resolve("a.example")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.TTL != 7*time.Second {
		t.Fatalf("ttl = %v, want min(S=7s,M=30s)=7s", res.TTL)
	}
}

// TTL 为 0 的记录参与本次解析但不写入缓存。
func TestZeroTTLNotCached(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		"z.example":  addrAnswer([]string{"9.9.9.9"}, 0),
		"zz.example": addrAnswer([]string{"8.8.8.8"}, 4*time.Second),
	})
	c, _ := newTestCache(t, 10, up.query, clock)

	res, err := c.Resolve("z.example")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !res.Found || res.TTL != 0 {
		t.Fatalf("got found=%v ttl=%v, want found=true ttl=0", res.Found, res.TTL)
	}
	if c.Len() != 0 {
		t.Fatalf("cache len = %d, zero-TTL record must not be stored", c.Len())
	}

	// 零 TTL 环与正 TTL 环混合：本次 TTL=0，且只有正 TTL 环入缓存。
	up.answers["mix.example"] = aliasAnswer("z.example", 10*time.Second)
	res, err = c.Resolve("mix.example")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.TTL != 0 {
		t.Fatalf("ttl = %v, want 0", res.TTL)
	}
	if c.Len() != 1 {
		t.Fatalf("cache len = %d, only the positive-TTL alias may be stored", c.Len())
	}

	// 再次解析：别名命中缓存，零 TTL 地址环仍需补查上游。
	_, _ = c.Resolve("mix.example")
	if got := up.calls; len(got) != 4 {
		t.Fatalf("calls = %v, want z.example re-queried each time", got)
	}

	// 负应答 min(S,M)=0 同样不缓存。
	up.answers["n.example"] = negAnswer(0, 10*time.Second)
	_, _ = c.Resolve("n.example")
	_, _ = c.Resolve("n.example")
	if up.callCount() != 6 {
		t.Fatalf("calls = %d, zero-TTL negative must not be cached", up.callCount())
	}
}

// 恰在到期时刻失效：now == expiresAt 视为未命中。
func TestExpiresExactlyAtInstant(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		"e.example": addrAnswer([]string{"1.0.0.1"}, 10*time.Second),
	})
	c, _ := newTestCache(t, 10, up.query, clock)

	if _, err := c.Resolve("e.example"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Len() != 1 {
		t.Fatal("entry should be cached")
	}
	clock.advance(10 * time.Second)
	if c.Len() != 0 {
		t.Fatal("entry must be invalid exactly at expiry")
	}

	// 到期后解析触发上游补查。
	_, _ = c.Resolve("e.example")
	if up.callCount() != 2 {
		t.Fatalf("calls = %d, want refetch after expiry", up.callCount())
	}
}

// 容量满时先清失效条目；仍满则淘汰到期最早者，并列取字典序小者。
func TestEvictionRules(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		"a.example": addrAnswer([]string{"a"}, 10*time.Second),
		"b.example": addrAnswer([]string{"b"}, 20*time.Second),
	})
	c, _ := newTestCache(t, 2, up.query, clock)

	_, _ = c.Resolve("a.example")
	_, _ = c.Resolve("b.example")

	// a 到期后插入 c：先清失效条目，无需淘汰存活条目。
	clock.advance(10 * time.Second)
	up.answers["c.example"] = addrAnswer([]string{"c"}, 30*time.Second)
	_, _ = c.Resolve("c.example")
	if c.Len() != 2 {
		t.Fatalf("len = %d, want 2 after purging expired", c.Len())
	}

	// 容量满且均存活：b 到期更早（exp=+20s，c exp=+40s），先淘汰 b。
	up.answers["d.example"] = addrAnswer([]string{"d"}, 30*time.Second)
	_, _ = c.Resolve("d.example")
	if c.Len() != 2 {
		t.Fatalf("len = %d, want 2 after eviction", c.Len())
	}
	c.mu.Lock()
	_, cAlive := c.entries["c.example"]
	_, dAlive := c.entries["d.example"]
	_, bAlive := c.entries["b.example"]
	c.mu.Unlock()
	if bAlive || !cAlive || !dAlive {
		t.Fatalf("want earliest-expiring b evicted; b=%v c=%v d=%v", bAlive, cAlive, dAlive)
	}

	// 此时 c 与 d 都在当前 +30s 到期（时刻并列）；插入 e 时
	// 并列淘汰字典序较小的 c。
	up.answers["e.example"] = addrAnswer([]string{"e"}, 100*time.Second)
	_, _ = c.Resolve("e.example")
	c.mu.Lock()
	_, cAlive = c.entries["c.example"]
	_, dAlive = c.entries["d.example"]
	_, eAlive := c.entries["e.example"]
	c.mu.Unlock()
	if cAlive || !dAlive || !eAlive {
		t.Fatalf("tie must evict lexicographically smaller c: c=%v d=%v e=%v", cAlive, dAlive, eAlive)
	}
}

// 同名记录替换：旧条目到期后，新到的同名记录直接替换，不额外占用容量。
func TestSameNameReplaces(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		"a.example": addrAnswer([]string{"old"}, 10*time.Second),
	})
	c, _ := newTestCache(t, 1, up.query, clock)
	_, _ = c.Resolve("a.example")

	clock.advance(10 * time.Second) // 旧记录恰到期，下次解析走上游
	up.answers["a.example"] = addrAnswer([]string{"new"}, 50*time.Second)
	res, err := c.Resolve("a.example")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if c.Len() != 1 {
		t.Fatalf("len = %d, replacement must not grow cache", c.Len())
	}
	if strings.Join(res.Addresses, ",") != "new" || res.TTL != 50*time.Second {
		t.Fatalf("got addrs=%v ttl=%v, want new/50s", res.Addresses, res.TTL)
	}
}

// 缓存与上游合起来成环即失败，且失败后缓存保持调用前状态。
func TestCycleLeavesCacheUntouched(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		// a -> b 已在缓存中（预置），上游给出 b -> a 形成“缓存+上游”合环。
		"b.example": aliasAnswer("a.example", 10*time.Second),
	})
	c, _ := newTestCache(t, 4, up.query, clock)
	c.mu.Lock()
	c.entries["a.example"] = &entry{
		kind: kindAlias, alias: "b.example", expiresAt: clock.now().Add(10 * time.Second),
	}
	c.mu.Unlock()

	before := snapshotCache(c)
	_, err := c.Resolve("a.example")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
	if !sameSnapshot(before, snapshotCache(c)) {
		t.Fatal("cache must remain unchanged after a cycle rejection")
	}

	// 完全由上游构成的环同样失败且不写入任何部分记录。
	up.answers["x.example"] = aliasAnswer("y.example", 10*time.Second)
	up.answers["y.example"] = aliasAnswer("z.example", 10*time.Second)
	up.answers["z.example"] = aliasAnswer("x.example", 10*time.Second)
	_, err = c.Resolve("x.example")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("err = %v, want ErrCycle", err)
	}
	if !sameSnapshot(before, snapshotCache(c)) {
		t.Fatal("partial upstream records must not be cached on cycle")
	}
}

// 别名记录超过 8 条即链过长；失败不污染缓存。
func TestChainTooLong(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{})
	names := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		names = append(names, "n"+string(rune('a'+i))+".example")
	}
	for i := 0; i < 9; i++ {
		up.answers[names[i]] = aliasAnswer(names[i+1], 30*time.Second)
	}
	up.answers[names[9]] = addrAnswer([]string{"10.0.0.1"}, 30*time.Second)
	c, _ := newTestCache(t, 20, up.query, clock)

	_, err := c.Resolve(names[0])
	if !errors.Is(err, ErrTooLong) {
		t.Fatalf("err = %v, want ErrTooLong", err)
	}
	if c.Len() != 0 {
		t.Fatalf("cache len = %d, rejected chain must cache nothing", c.Len())
	}

	// 恰好 8 条别名是允许的：n0..n7 为别名，n8 为地址终点。
	short := newScripted(map[string]UpstreamAnswer{})
	for i := 0; i < 8; i++ {
		short.answers[names[i]] = aliasAnswer(names[i+1], 30*time.Second)
	}
	short.answers[names[8]] = addrAnswer([]string{"10.0.0.2"}, 30*time.Second)
	c2 := New(20, short.query, WithClock(clock.now))
	res, err := c2.Resolve(names[0])
	if err != nil {
		t.Fatalf("8-alias chain should be accepted, got %v", err)
	}
	if len(res.Aliases) != 8 {
		t.Fatalf("aliases = %d, want 8", len(res.Aliases))
	}
}

// 空名字最先被拒绝，不访问上游，缓存不变。
func TestEmptyNameRejectedFirst(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{})
	c, _ := newTestCache(t, 4, up.query, clock)

	_, err := c.Resolve("")
	if !errors.Is(err, ErrEmptyName) {
		t.Fatalf("err = %v, want ErrEmptyName", err)
	}
	if up.callCount() != 0 {
		t.Fatalf("upstream calls = %d, empty name must not reach upstream", up.callCount())
	}
	if c.Len() != 0 {
		t.Fatal("cache must stay empty")
	}
}

// 上游失败不污染缓存；同一名字的并发等待者拿到同一错误。
func TestUpstreamFailureDoesNotPolluteCache(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		"a.example": aliasAnswer("b.example", 10*time.Second),
	})
	up.errs["b.example"] = errors.New("upstream unavailable")
	c, _ := newTestCache(t, 4, up.query, clock)

	before := snapshotCache(c)
	_, err := c.Resolve("a.example")
	if err == nil {
		t.Fatal("want upstream error")
	}
	if !sameSnapshot(before, snapshotCache(c)) {
		t.Fatal("upstream failure must leave cache unchanged")
	}

	// 修复上游后重试应成功（之前的部分结果未被缓存）。
	delete(up.errs, "b.example")
	up.answers["b.example"] = addrAnswer([]string{"5.5.5.5"}, 10*time.Second)
	res, err := c.Resolve("a.example")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !res.Found || strings.Join(res.Addresses, ",") != "5.5.5.5" {
		t.Fatalf("retry result = %+v", res)
	}
}

// 并发解析同一未命中名字时只向上游查询一次，等待者得到相同记录。
func TestConcurrentResolveSingleUpstreamCall(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		"hot.example": addrAnswer([]string{"7.7.7.7", "7.7.7.8"}, 25*time.Second),
	})
	started := make(chan string, 16)
	up.started = started
	release := make(chan struct{})
	up.release["hot.example"] = release
	c := New(3, up.query, WithClock(clock.now))

	const n = 16
	var wg sync.WaitGroup
	results := make([]Result, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = c.Resolve("hot.example")
		}(i)
	}
	<-started // 等待唯一的上游调用开始
	for c.inflightWaiters("hot.example") != n-1 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()

	if up.callCount() != 1 {
		t.Fatalf("upstream calls = %d, want exactly 1", up.callCount())
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d error: %v", i, errs[i])
		}
		if !results[i].Found || results[i].TTL != 25*time.Second ||
			strings.Join(results[i].Addresses, ",") != "7.7.7.7,7.7.7.8" {
			t.Fatalf("goroutine %d got %+v, want identical result", i, results[i])
		}
	}
	if c.Len() != 1 {
		t.Fatalf("cache len = %d, want 1", c.Len())
	}
}

// 并发时上游失败，所有等待者得到同一错误，缓存不增加条目。
func TestConcurrentResolveSharedError(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{})
	up.errs["boom.example"] = errors.New("upstream boom")
	started := make(chan string, 16)
	up.started = started
	release := make(chan struct{})
	up.release["boom.example"] = release
	c := New(3, up.query, WithClock(clock.now))

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = c.Resolve("boom.example")
		}(i)
	}
	<-started
	for c.inflightWaiters("boom.example") != n-1 {
		time.Sleep(time.Millisecond)
	}
	close(release)
	wg.Wait()

	if up.callCount() != 1 {
		t.Fatalf("upstream calls = %d, want exactly 1", up.callCount())
	}
	for i, err := range errs {
		if err == nil || err.Error() != "upstream boom" {
			t.Fatalf("goroutine %d err = %v, want shared upstream error", i, err)
		}
	}
	if c.Len() != 0 {
		t.Fatalf("cache len = %d, failed resolution must not be cached", c.Len())
	}
}

// 并发解析同一未命中名字（链长为 3）：链头只查一次，
// 等待者共享整条解析，链上每个名字都只向上游查询一次。
func TestConcurrentChainEachHopQueriedOnce(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		"a.example": aliasAnswer("b.example", 10*time.Second),
		"b.example": aliasAnswer("c.example", 10*time.Second),
		"c.example": addrAnswer([]string{"4.4.4.4"}, 10*time.Second),
	})
	started := make(chan string, 32)
	up.started = started
	gate := make(chan struct{})
	up.release["a.example"] = gate
	c := New(10, up.query, WithClock(clock.now))

	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = c.Resolve("a.example")
		}(i)
	}
	<-started // 链头唯一的上游调用开始
	for c.inflightWaiters("a.example") != n-1 {
		time.Sleep(time.Millisecond)
	}
	close(gate)
	wg.Wait()

	// 排空链上后续环的 started 通知
	for i := 0; i < 2; i++ {
		<-started
	}
	for _, err := range errs {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if up.callCount() != 3 {
		t.Fatalf("upstream calls = %d, want exactly 3 (one per chain link)", up.callCount())
	}
	if c.Len() != 3 {
		t.Fatalf("cache len = %d, want 3 chain entries", c.Len())
	}
}

// 相同调用与上游应答序列在相同时钟下重放，结果完全相同。
func TestDeterministicReplay(t *testing.T) {
	script := map[string]UpstreamAnswer{
		"a.example": aliasAnswer("b.example", 10*time.Second),
		"b.example": aliasAnswer("c.example", 30*time.Second),
		"c.example": addrAnswer([]string{"1.1.1.1", "1.1.1.0"}, 20*time.Second),
	}
	run := func() (Result, []string) {
		clock := newManualClock()
		up := newScripted(script)
		c := New(10, up.query, WithClock(clock.now))
		r, err := c.Resolve("a.example")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		clock.advance(3 * time.Second)
		r, err = c.Resolve("a.example")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		return r, up.calls
	}

	r1, calls1 := run()
	r2, calls2 := run()
	if !resultEqual(r1, r2) {
		t.Fatalf("replay mismatch: %+v vs %+v", r1, r2)
	}
	if strings.Join(calls1, ",") != strings.Join(calls2, ",") {
		t.Fatalf("upstream call sequence mismatch: %v vs %v", calls1, calls2)
	}
	if r1.TTL != 7*time.Second {
		t.Fatalf("ttl = %v, want 7s", r1.TTL)
	}
}

// 日志打印输入、输出与判定依据。
func TestLoggingShowsInputOutputAndReasoning(t *testing.T) {
	clock := newManualClock()
	up := newScripted(map[string]UpstreamAnswer{
		"a.example": aliasAnswer("b.example", 10*time.Second),
		"b.example": addrAnswer([]string{"1.1.1.1"}, 20*time.Second),
	})
	c, logBuf := newTestCache(t, 10, up.query, clock)
	if _, err := c.Resolve("a.example"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	logText := logBuf.String()
	for _, want := range []string{
		"resolve input name=\"a.example\"",
		"cache miss, querying upstream",
		"decision: terminal address record",
		"min remaining ttl=10s",
		"resolve output name=\"a.example\" found=true",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q\n--- log ---\n%s", want, logText)
		}
	}

	// 拒绝路径同样记录输入、判定依据与输出错误。
	logBuf.Reset()
	_, err := c.Resolve("")
	if err == nil {
		t.Fatal("want error")
	}
	rejectLog := logBuf.String()
	for _, want := range []string{
		"resolve input name=\"\"",
		"reject: name is empty",
		"resolve output name=\"\" error=",
	} {
		if !strings.Contains(rejectLog, want) {
			t.Fatalf("reject log missing %q\n--- log ---\n%s", want, rejectLog)
		}
	}
}
