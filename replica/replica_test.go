package replica

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/version"
)

// testBackend 是内存后端桩：保存键值与当前版本，统计回源调用次数。
type testBackend struct {
	mu    sync.Mutex
	ver   version.Version
	vals  map[string]string
	calls int64
	hook  func(key string) // 每次回源时调用（测试注入时序用）
}

func newBackend() *testBackend {
	return &testBackend{vals: map[string]string{}}
}

func (b *testBackend) write(key, val string) version.Version {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ver++
	b.vals[key] = val
	return b.ver
}

func (b *testBackend) load(ctx context.Context, key string) (string, version.Version, bool, error) {
	b.mu.Lock()
	b.calls++
	hook := b.hook
	ver := b.ver
	val, ok := b.vals[key]
	b.mu.Unlock()
	if hook != nil {
		hook(key)
	}
	return val, ver, ok, nil
}

func (b *testBackend) loadCalls() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func newTestReplica(b *testBackend, ttl time.Duration) (*Replica, *ManualClock) {
	clock := NewManualClock()
	r := New(Config{TTL: ttl, Loader: b.load, Clock: clock.Clock()})
	return r, clock
}

func mustGet(t *testing.T, r *Replica, key string) (string, bool) {
	t.Helper()
	v, found, err := r.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("Get(%s): %v", key, err)
	}
	return v, found
}

func TestGetFillThenHit(t *testing.T) {
	b := newBackend()
	b.write("k", "v1")
	r, _ := newTestReplica(b, 100)
	v, found := mustGet(t, r, "k")
	if v != "v1" || !found {
		t.Fatalf("got %q,%v want v1,true", v, found)
	}
	mustGet(t, r, "k")
	if got := b.loadCalls(); got != 1 {
		t.Fatalf("loader called %d times, want 1 (second read must hit)", got)
	}
	info := r.Inspect("k")
	if info.State.String() != "Valid" || info.Version != 1 || info.Hits != 1 || info.Misses != 1 {
		t.Fatalf("unexpected inspect: %+v", info)
	}
}

func TestExpiryBoundaryLeftClosedRightOpen(t *testing.T) {
	b := newBackend()
	b.write("k", "v1")
	r, clock := newTestReplica(b, 10)
	mustGet(t, r, "k") // 填入于 t=0，到期时刻 t=10
	clock.Set(9)
	mustGet(t, r, "k")
	if got := b.loadCalls(); got != 1 {
		t.Fatalf("t=9 must still hit, loader calls=%d", got)
	}
	clock.Set(10) // 恰好到期即过期
	mustGet(t, r, "k")
	if got := b.loadCalls(); got != 2 {
		t.Fatalf("t=10 must refetch, loader calls=%d", got)
	}
	if info := r.Inspect("k"); info.TTLRemaining != 10 {
		t.Fatalf("refilled TTL=%v want 10", info.TTLRemaining)
	}
}

func TestFailureNotCachedAndRetryable(t *testing.T) {
	boom := errors.New("backend down")
	calls := 0
	loader := func(ctx context.Context, key string) (string, version.Version, bool, error) {
		calls++
		if calls == 1 {
			return "", 0, false, boom
		}
		return "v", 1, true, nil
	}
	clock := NewManualClock()
	r := New(Config{TTL: 100, Loader: loader, Clock: clock.Clock()})
	if _, _, err := r.Get(context.Background(), "k"); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
	if info := r.Inspect("k"); info.State.String() != "Hole" || info.Version != 0 {
		t.Fatalf("failed fetch must not write cache: %+v", info)
	}
	v, found, err := r.Get(context.Background(), "k")
	if err != nil || v != "v" || !found {
		t.Fatalf("retry got %q,%v,%v", v, found, err)
	}
	if calls != 2 {
		t.Fatalf("loader calls=%d want 2 (failure must not be cached)", calls)
	}
}

func TestHoleVsNotFoundDistinguishable(t *testing.T) {
	b := newBackend() // "ghost" 不存在于后端
	r, _ := newTestReplica(b, 100)
	_, found := mustGet(t, r, "ghost")
	if found {
		t.Fatal("ghost must not be found")
	}
	neg := r.Inspect("ghost")
	if !neg.Exists || neg.State.String() != "Valid" || neg.Found {
		t.Fatalf("negative cache entry: %+v", neg)
	}
	hole := r.Inspect("never-touched")
	if hole != (KeyInfo{}) {
		t.Fatalf("unknown key must be zero value, got %+v", hole)
	}
	if neg == hole {
		t.Fatal("not-found and never-fetched must be distinguishable")
	}
}

func TestInspectStableAndReadOnly(t *testing.T) {
	b := newBackend()
	b.write("k", "v1")
	r, clock := newTestReplica(b, 10)
	mustGet(t, r, "k")
	a, b2 := r.Inspect("k"), r.Inspect("k")
	if a != b2 {
		t.Fatal("two consecutive inspects must be identical")
	}
	clock.Set(10) // 到期后：惰性过期只影响视图，两次查询仍一致
	c, d := r.Inspect("k"), r.Inspect("k")
	if c != d {
		t.Fatal("expired inspects must be identical")
	}
	if c.State.String() != "Stale" || c.TTLRemaining != 0 {
		t.Fatalf("expired view: %+v", c)
	}
	if got := b.loadCalls(); got != 1 {
		t.Fatalf("inspect must not trigger fetch, calls=%d", got)
	}
}
