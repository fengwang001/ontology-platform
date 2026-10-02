package resolver

import (
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock for deterministic tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// script is a programmable upstream recording per-name call counts.
type script struct {
	mu      sync.Mutex
	answers map[string]Answer
	errs    map[string]error
	calls   map[string]int
	delay   time.Duration
}

func newScript() *script {
	return &script{
		answers: make(map[string]Answer),
		errs:    make(map[string]error),
		calls:   make(map[string]int),
	}
}

func (s *script) upstream(name string) (Answer, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[name]++
	if err, ok := s.errs[name]; ok {
		return Answer{}, err
	}
	ans, ok := s.answers[name]
	if !ok {
		return Answer{}, fmt.Errorf("no scripted answer for %q", name)
	}
	return ans, nil
}

func (s *script) callsFor(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[name]
}

func alias(target string, ttl time.Duration) Answer {
	return Answer{Kind: AnswerAlias, Target: target, TTL: ttl}
}

func addr(ttl time.Duration, addrs ...string) Answer {
	return Answer{Kind: AnswerAddress, Addrs: addrs, TTL: ttl}
}

func negative(s, m time.Duration) Answer {
	return Answer{Kind: AnswerNegative, AuthorityTTL: s, Minimum: m}
}

// testLogger routes resolver decision logs into the test log.
type testLogger struct{ t *testing.T }

func (w testLogger) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func newResolver(t *testing.T, s *script, capacity int, clock *fakeClock) *Resolver {
	t.Helper()
	return New(s.upstream, capacity,
		WithClock(clock.Now),
		WithLogger(log.New(testLogger{t}, "resolver ", 0)))
}

// TestMinTTLAcrossChain verifies that the returned TTL is the minimum
// remaining lifetime over all links of the chain.
func TestMinTTLAcrossChain(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	s.answers["a"] = alias("b", 100*time.Second)
	s.answers["b"] = addr(30*time.Second, "10.0.0.1")
	r := newResolver(t, s, 16, clock)

	// Seed the cache with b's address record, then let 10s elapse so
	// b has only 20s remaining when the chain is resolved.
	res, err := r.Resolve("b")
	if err != nil {
		t.Fatalf("seed resolve: %v", err)
	}
	clock.Advance(10 * time.Second)

	res, err = r.Resolve("a")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	t.Logf("input: resolve(a) with a->b ttl=100s fresh, b cached with 20s remaining")
	t.Logf("output: chain=%v addrs=%v ttl=%s", res.Chain, res.Addrs, res.TTL)
	t.Logf("rationale: ttl must be min(100s fresh, 20s remaining) = 20s")

	if want := 20 * time.Second; res.TTL != want {
		t.Errorf("ttl = %s, want %s", res.TTL, want)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(res.Chain, want) {
		t.Errorf("chain = %v, want %v", res.Chain, want)
	}
	if want := []string{"10.0.0.1"}; !reflect.DeepEqual(res.Addrs, want) {
		t.Errorf("addrs = %v, want %v", res.Addrs, want)
	}
	if got := s.callsFor("b"); got != 1 {
		t.Errorf("upstream calls for b = %d, want 1 (cache hit)", got)
	}
}

// TestExpiredAddressLinkRefetched verifies that when the alias link is
// still cached but the address link has expired, only the address link
// is re-queried upstream.
func TestExpiredAddressLinkRefetched(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	s.answers["a"] = alias("b", 100*time.Second)
	s.answers["b"] = addr(10*time.Second, "10.0.0.1")
	r := newResolver(t, s, 16, clock)

	if _, err := r.Resolve("a"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	// Advance exactly to b's expiry: b is now invalid, a still valid.
	clock.Advance(10 * time.Second)

	res, err := r.Resolve("a")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	t.Logf("input: resolve(a) with a->b cached (90s left), b expired at t+10s")
	t.Logf("output: chain=%v addrs=%v ttl=%s", res.Chain, res.Addrs, res.TTL)
	t.Logf("rationale: only the expired address link b is re-queried; ttl = min(90s, 10s) = 10s")

	if got := s.callsFor("a"); got != 1 {
		t.Errorf("upstream calls for a = %d, want 1 (alias link served from cache)", got)
	}
	if got := s.callsFor("b"); got != 2 {
		t.Errorf("upstream calls for b = %d, want 2 (address link re-queried)", got)
	}
	if want := 10 * time.Second; res.TTL != want {
		t.Errorf("ttl = %s, want %s", res.TTL, want)
	}
}

// TestNegativeTTLMinSM verifies that negative entries live for
// min(S, M) and that a cached negative answer is reused.
func TestNegativeTTLMinSM(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	s.answers["missing"] = negative(50*time.Second, 30*time.Second)
	r := newResolver(t, s, 16, clock)

	res, err := r.Resolve("missing")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	t.Logf("input: resolve(missing), upstream negative S=50s M=30s")
	t.Logf("output: negative=%v ttl=%s", res.Negative, res.TTL)
	t.Logf("rationale: negative ttl must be min(S, M) = 30s")

	if !res.Negative {
		t.Error("expected negative result")
	}
	if want := 30 * time.Second; res.TTL != want {
		t.Errorf("ttl = %s, want %s", res.TTL, want)
	}

	clock.Advance(10 * time.Second)
	res, err = r.Resolve("missing")
	if err != nil {
		t.Fatalf("cached resolve: %v", err)
	}
	t.Logf("input: resolve(missing) again after 10s")
	t.Logf("output: negative=%v ttl=%s", res.Negative, res.TTL)
	t.Logf("rationale: cached negative entry has 30s-10s = 20s remaining, no upstream call")

	if want := 20 * time.Second; res.TTL != want {
		t.Errorf("cached ttl = %s, want %s", res.TTL, want)
	}
	if got := s.callsFor("missing"); got != 1 {
		t.Errorf("upstream calls = %d, want 1 (negative entry cached)", got)
	}
}

// TestZeroTTLNotCached verifies that zero-TTL records are used for the
// current resolution but never stored.
func TestZeroTTLNotCached(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	s.answers["a"] = alias("b", 60*time.Second)
	s.answers["b"] = addr(0, "10.0.0.1")
	r := newResolver(t, s, 16, clock)

	res, err := r.Resolve("a")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	t.Logf("input: resolve(a), a->b ttl=60s, b address ttl=0")
	t.Logf("output: chain=%v addrs=%v ttl=%s", res.Chain, res.Addrs, res.TTL)
	t.Logf("rationale: zero-TTL record joins this resolution (ttl=0) but is not cached")

	if want := time.Duration(0); res.TTL != want {
		t.Errorf("ttl = %s, want 0", res.TTL)
	}
	if want := []string{"10.0.0.1"}; !reflect.DeepEqual(res.Addrs, want) {
		t.Errorf("addrs = %v, want %v", res.Addrs, want)
	}

	if _, err := r.Resolve("a"); err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if got := s.callsFor("b"); got != 2 {
		t.Errorf("upstream calls for b = %d, want 2 (zero TTL never cached)", got)
	}
	if got := s.callsFor("a"); got != 1 {
		t.Errorf("upstream calls for a = %d, want 1 (positive alias cached)", got)
	}
}

// TestExpiryBoundary verifies that an entry is invalid exactly at its
// expiry time (now >= expiresAt).
func TestExpiryBoundary(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	s.answers["a"] = addr(10*time.Second, "10.0.0.1")
	r := newResolver(t, s, 16, clock)

	if _, err := r.Resolve("a"); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	clock.Advance(9 * time.Second)
	if _, err := r.Resolve("a"); err != nil {
		t.Fatalf("resolve at t+9s: %v", err)
	}
	t.Logf("input: resolve(a) at t+9s with entry expiring at t+10s")
	t.Logf("rationale: now < expiry, entry still valid, no upstream call")
	if got := s.callsFor("a"); got != 1 {
		t.Errorf("upstream calls at t+9s = %d, want 1 (still cached)", got)
	}

	clock.Advance(1 * time.Second)
	res, err := r.Resolve("a")
	if err != nil {
		t.Fatalf("resolve at t+10s: %v", err)
	}
	t.Logf("input: resolve(a) at t+10s, exactly the expiry moment")
	t.Logf("output: ttl=%s", res.TTL)
	t.Logf("rationale: now >= expiry, entry invalid, upstream re-queried")
	if got := s.callsFor("a"); got != 2 {
		t.Errorf("upstream calls at t+10s = %d, want 2 (expired exactly at expiry)", got)
	}
	if want := 10 * time.Second; res.TTL != want {
		t.Errorf("ttl = %s, want %s (fresh record)", res.TTL, want)
	}
}

// TestLoopFailureCacheUnchanged verifies that a resolution rejected for
// a chain loop leaves the cache exactly as it was before the call,
// discarding any records fetched along the way.
func TestLoopFailureCacheUnchanged(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	s.answers["keep"] = addr(100*time.Second, "10.0.0.9")
	s.answers["x"] = alias("y", 50*time.Second)
	s.answers["y"] = alias("x", 50*time.Second)
	r := newResolver(t, s, 16, clock)

	if _, err := r.Resolve("keep"); err != nil {
		t.Fatalf("seed resolve: %v", err)
	}
	sizeBefore := r.Len()

	_, err := r.Resolve("x")
	t.Logf("input: resolve(x) with x->y->x loop")
	t.Logf("output: err=%v", err)
	t.Logf("rationale: loop detected, cache must stay unchanged (fetched x,y discarded)")
	if !errors.Is(err, ErrChainLoop) {
		t.Fatalf("err = %v, want ErrChainLoop", err)
	}
	if got := r.Len(); got != sizeBefore {
		t.Errorf("cache size = %d, want %d (unchanged after rejection)", got, sizeBefore)
	}

	// The pre-existing entry must still be served from cache, and the
	// loop records must not have been cached.
	if _, err := r.Resolve("keep"); err != nil {
		t.Fatalf("resolve keep: %v", err)
	}
	if got := s.callsFor("keep"); got != 1 {
		t.Errorf("upstream calls for keep = %d, want 1 (entry preserved)", got)
	}
	if _, err := r.Resolve("x"); !errors.Is(err, ErrChainLoop) {
		t.Fatalf("second resolve err = %v, want ErrChainLoop", err)
	}
	if got := s.callsFor("x"); got != 2 {
		t.Errorf("upstream calls for x = %d, want 2 (loop records not cached)", got)
	}
}

// TestChainTooLong verifies that chains with more than MaxAliases alias
// records are rejected and the cache stays unchanged.
func TestChainTooLong(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	// n0 -> n1 -> ... -> n9 -> addr: 9 aliases, one too many.
	for i := 0; i < 9; i++ {
		s.answers[fmt.Sprintf("n%d", i)] = alias(fmt.Sprintf("n%d", i+1), 60*time.Second)
	}
	s.answers["n9"] = addr(60*time.Second, "10.0.0.1")
	r := newResolver(t, s, 16, clock)

	_, err := r.Resolve("n0")
	t.Logf("input: resolve(n0) with a chain of 9 alias records")
	t.Logf("output: err=%v", err)
	t.Logf("rationale: more than %d alias records means the chain is too long", MaxAliases)
	if !errors.Is(err, ErrChainTooLong) {
		t.Fatalf("err = %v, want ErrChainTooLong", err)
	}
	if got := r.Len(); got != 0 {
		t.Errorf("cache size = %d, want 0 (unchanged after rejection)", got)
	}

	// Exactly 8 aliases followed by an address record resolves fine.
	s2 := newScript()
	for i := 0; i < 8; i++ {
		s2.answers[fmt.Sprintf("m%d", i)] = alias(fmt.Sprintf("m%d", i+1), 60*time.Second)
	}
	s2.answers["m8"] = addr(60*time.Second, "10.0.0.2")
	r2 := newResolver(t, s2, 16, clock)
	res, err := r2.Resolve("m0")
	if err != nil {
		t.Fatalf("resolve with %d aliases: %v", MaxAliases, err)
	}
	t.Logf("input: resolve(m0) with exactly %d alias records", MaxAliases)
	t.Logf("output: chain=%v ttl=%s", res.Chain, res.TTL)
	t.Logf("rationale: %d aliases is within the limit, resolution succeeds", MaxAliases)
	if want := 9; len(res.Chain) != want {
		t.Errorf("chain length = %d, want %d", len(res.Chain), want)
	}
}

// TestEmptyNameRejectedFirst verifies that an empty name is rejected
// before anything else and leaves the cache unchanged.
func TestEmptyNameRejectedFirst(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	s.answers["a"] = addr(60*time.Second, "10.0.0.1")
	r := newResolver(t, s, 16, clock)

	if _, err := r.Resolve("a"); err != nil {
		t.Fatalf("seed resolve: %v", err)
	}
	_, err := r.Resolve("")
	t.Logf("input: resolve(\"\")")
	t.Logf("output: err=%v", err)
	t.Logf("rationale: empty name is the first rejection, cache unchanged")
	if !errors.Is(err, ErrEmptyName) {
		t.Fatalf("err = %v, want ErrEmptyName", err)
	}
	if got := r.Len(); got != 1 {
		t.Errorf("cache size = %d, want 1 (unchanged)", got)
	}
}

// TestUpstreamErrorNotCached verifies that upstream failures reject the
// resolution without polluting the cache, and a later retry succeeds.
func TestUpstreamErrorNotCached(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	s.answers["ok"] = addr(60*time.Second, "10.0.0.1")
	s.answers["bad"] = addr(60*time.Second, "10.0.0.2")
	s.errs["bad"] = errors.New("upstream timeout")
	r := newResolver(t, s, 16, clock)

	if _, err := r.Resolve("ok"); err != nil {
		t.Fatalf("seed resolve: %v", err)
	}
	_, err := r.Resolve("bad")
	t.Logf("input: resolve(bad) with failing upstream")
	t.Logf("output: err=%v", err)
	t.Logf("rationale: upstream failure rejects the call and is not cached")
	if err == nil {
		t.Fatal("expected upstream error")
	}
	if got := r.Len(); got != 1 {
		t.Errorf("cache size = %d, want 1 (failure not cached)", got)
	}

	delete(s.errs, "bad")
	res, err := r.Resolve("bad")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if want := []string{"10.0.0.2"}; !reflect.DeepEqual(res.Addrs, want) {
		t.Errorf("addrs = %v, want %v", res.Addrs, want)
	}
}

// TestEviction verifies capacity handling: expired entries are purged
// first, then the entry with the earliest expiry is evicted, ties
// broken by the lexicographically smaller name.
func TestEviction(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	s.answers["old"] = addr(10*time.Second, "10.0.0.1")
	s.answers["new"] = addr(100*time.Second, "10.0.0.2")
	s.answers["third"] = addr(100*time.Second, "10.0.0.3")
	r := newResolver(t, s, 2, clock)

	if _, err := r.Resolve("old"); err != nil {
		t.Fatalf("resolve old: %v", err)
	}
	if _, err := r.Resolve("new"); err != nil {
		t.Fatalf("resolve new: %v", err)
	}
	if _, err := r.Resolve("third"); err != nil {
		t.Fatalf("resolve third: %v", err)
	}
	t.Logf("input: capacity=2, store old(exp t+10s), new(t+100s), third(t+100s)")
	t.Logf("rationale: storing third evicts old, the earliest expiring entry")
	if got := r.Len(); got != 2 {
		t.Fatalf("cache size = %d, want 2", got)
	}
	if _, err := r.Resolve("old"); err != nil {
		t.Fatalf("re-resolve old: %v", err)
	}
	if got := s.callsFor("old"); got != 2 {
		t.Errorf("upstream calls for old = %d, want 2 (evicted)", got)
	}
	if got := s.callsFor("new"); got != 1 {
		t.Errorf("upstream calls for new = %d, want 1 (kept)", got)
	}

	// Tie: same expiry, evict the lexicographically smaller name.
	clock2 := newFakeClock()
	s2 := newScript()
	s2.answers["beta"] = addr(50*time.Second, "10.0.1.1")
	s2.answers["alpha"] = addr(50*time.Second, "10.0.1.2")
	s2.answers["gamma"] = addr(50*time.Second, "10.0.1.3")
	r2 := newResolver(t, s2, 2, clock2)
	for _, n := range []string{"beta", "alpha", "gamma"} {
		if _, err := r2.Resolve(n); err != nil {
			t.Fatalf("resolve %s: %v", n, err)
		}
	}
	t.Logf("input: capacity=2, store beta, alpha, gamma all expiring at t+50s")
	t.Logf("rationale: equal expiry, evict lexicographically smaller name alpha")
	if _, err := r2.Resolve("alpha"); err != nil {
		t.Fatalf("re-resolve alpha: %v", err)
	}
	if got := s2.callsFor("alpha"); got != 2 {
		t.Errorf("upstream calls for alpha = %d, want 2 (evicted on tie)", got)
	}
	if got := s2.callsFor("beta"); got != 1 {
		t.Errorf("upstream calls for beta = %d, want 1 (kept on tie)", got)
	}

	// Expired entries are purged before any eviction is needed.
	clock3 := newFakeClock()
	s3 := newScript()
	s3.answers["dead"] = addr(5*time.Second, "10.0.2.1")
	s3.answers["live"] = addr(100*time.Second, "10.0.2.2")
	s3.answers["next"] = addr(100*time.Second, "10.0.2.3")
	r3 := newResolver(t, s3, 2, clock3)
	if _, err := r3.Resolve("dead"); err != nil {
		t.Fatalf("resolve dead: %v", err)
	}
	if _, err := r3.Resolve("live"); err != nil {
		t.Fatalf("resolve live: %v", err)
	}
	clock3.Advance(5 * time.Second) // dead expires exactly
	if _, err := r3.Resolve("next"); err != nil {
		t.Fatalf("resolve next: %v", err)
	}
	t.Logf("input: capacity=2, dead expired at t+5s, store next")
	t.Logf("rationale: expired dead is purged first, live is not evicted")
	if _, err := r3.Resolve("live"); err != nil {
		t.Fatalf("re-resolve live: %v", err)
	}
	if got := s3.callsFor("live"); got != 1 {
		t.Errorf("upstream calls for live = %d, want 1 (purge before eviction)", got)
	}
}

// TestConcurrentSingleUpstreamQuery verifies that concurrent resolvers
// of the same uncached name trigger exactly one upstream query and all
// observe the same result.
func TestConcurrentSingleUpstreamQuery(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	s.delay = 20 * time.Millisecond // widen the race window
	s.answers["a"] = alias("b", 60*time.Second)
	s.answers["b"] = addr(60*time.Second, "10.0.0.1")
	r := newResolver(t, s, 16, clock)

	const workers = 16
	results := make([]Result, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = r.Resolve("a")
		}(i)
	}
	close(start)
	wg.Wait()

	t.Logf("input: %d goroutines resolve(a) simultaneously, cache cold", workers)
	t.Logf("output: upstream calls for a = %d, for b = %d", s.callsFor("a"), s.callsFor("b"))
	t.Logf("rationale: single-flight, exactly one upstream query per name")

	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	for i := 1; i < workers; i++ {
		if !reflect.DeepEqual(results[i], results[0]) {
			t.Fatalf("worker %d result %+v differs from %+v", i, results[i], results[0])
		}
	}
	if got := s.callsFor("a"); got != 1 {
		t.Errorf("upstream calls for a = %d, want 1", got)
	}
	if got := s.callsFor("b"); got != 1 {
		t.Errorf("upstream calls for b = %d, want 1", got)
	}
	if got := r.Len(); got > 16 {
		t.Errorf("cache size = %d exceeds capacity", got)
	}
}

// TestConcurrentUpstreamErrorShared verifies that waiters on an
// in-flight query observe the same upstream error.
func TestConcurrentUpstreamErrorShared(t *testing.T) {
	clock := newFakeClock()
	s := newScript()
	s.delay = 20 * time.Millisecond
	s.errs["bad"] = errors.New("upstream boom")
	r := newResolver(t, s, 16, clock)

	const workers = 8
	errs := make([]error, workers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = r.Resolve("bad")
		}(i)
	}
	close(start)
	wg.Wait()

	t.Logf("input: %d goroutines resolve(bad), upstream fails", workers)
	t.Logf("output: upstream calls = %d, all errors identical", s.callsFor("bad"))
	t.Logf("rationale: single-flight shares the same error, failure not cached")

	if got := s.callsFor("bad"); got != 1 {
		t.Errorf("upstream calls = %d, want 1", got)
	}
	for i, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "upstream boom") {
			t.Errorf("worker %d err = %v, want shared upstream error", i, err)
		}
	}
	if got := r.Len(); got != 0 {
		t.Errorf("cache size = %d, want 0 (failure not cached)", got)
	}
}

// TestReplayDeterministic verifies that the same call sequence with the
// same upstream answers replays identically.
func TestReplayDeterministic(t *testing.T) {
	run := func() []string {
		clock := newFakeClock()
		s := newScript()
		s.answers["a"] = alias("b", 40*time.Second)
		s.answers["b"] = addr(30*time.Second, "10.0.0.1")
		s.answers["c"] = negative(20*time.Second, 10*time.Second)
		r := New(s.upstream, 4, WithClock(clock.Now))

		var out []string
		seq := []string{"a", "c", "a", "b", "c"}
		for _, name := range seq {
			res, err := r.Resolve(name)
			if err != nil {
				out = append(out, "err: "+err.Error())
				continue
			}
			out = append(out, fmt.Sprintf("chain=%v addrs=%v neg=%v ttl=%s",
				res.Chain, res.Addrs, res.Negative, res.TTL))
			clock.Advance(5 * time.Second)
		}
		return out
	}

	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Fatalf("replay %d differs:\nfirst: %v\ngot:   %v", i, first, got)
		}
	}
	t.Logf("input: fixed resolve sequence [a c a b c] with 5s advances")
	t.Logf("output: %v", first)
	t.Logf("rationale: identical call and answer sequences replay identically")
}
