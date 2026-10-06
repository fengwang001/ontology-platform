package ecscache

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------- 测试辅助 ----------

// fakeClock 为可手动推进的注入时钟（并发安全）。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// recorder 记录上游请求并委托给脚本函数。
type recorder struct {
	mu    sync.Mutex
	calls []Request
	fn    ResolverFunc
}

func (r *recorder) Resolve(ctx context.Context, req Request) (Response, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	r.mu.Unlock()
	return r.fn(ctx, req)
}

func (r *recorder) n() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func v4(a, b, c, d byte) []byte { return []byte{a, b, c, d} }

func v6(prefix ...byte) []byte {
	out := make([]byte, 16)
	copy(out, prefix)
	return out
}

func recs(s ...string) Result { return Result{Kind: KindRecords, Records: s} }

func staticUpstream(resp Response) ResolverFunc {
	return func(context.Context, Request) (Response, error) { return resp, nil }
}

func mustResolve(t *testing.T, c *Cache, q Query) Result {
	t.Helper()
	res, err := c.Resolve(context.Background(), q)
	if err != nil {
		t.Fatalf("Resolve(%+v) unexpected error: %v", q, err)
	}
	return res
}

// ---------- 单元测试 ----------

// 生存时间的恰到期边界：写入时刻起 TTL 内有效，到期时刻本身已失效（左闭右开）。
func TestTTLExactExpiryBoundary(t *testing.T) {
	fc := newFakeClock()
	var up *recorder
	up = &recorder{fn: func(_ context.Context, _ Request) (Response, error) {
		return Response{Result: recs(fmt.Sprintf("call-%d", up.n())), TTL: 10, Scope: 24}, nil
	}}
	c := New(4, fc.now, up)
	q := Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 1, 2, 3), SourcePrefixLen: 24}

	if got := mustResolve(t, c, q); got.Records[0] != "call-1" {
		t.Fatalf("first resolve = %v", got)
	}
	fc.advance(9 * time.Second) // t0+9：仍有效
	if got := mustResolve(t, c, q); got.Records[0] != "call-1" || up.n() != 1 {
		t.Fatalf("t0+9 should hit cache: got=%v calls=%d", got, up.n())
	}
	fc.advance(time.Second) // t0+10：恰到期，已失效
	if got := mustResolve(t, c, q); got.Records[0] != "call-2" || up.n() != 2 {
		t.Fatalf("t0+10 must miss and re-query: got=%v calls=%d", got, up.n())
	}
}

// 较长范围条目过期后，应回落到较短范围的未过期条目，而不是判未命中。
func TestFallbackToShorterScopeAfterExpiry(t *testing.T) {
	fc := newFakeClock()
	up := &recorder{fn: func(_ context.Context, req Request) (Response, error) {
		// 第一次（/24 查询）给短 TTL 的长范围；第二次（/0 查询）给长 TTL 的零范围。
		if req.SourcePrefixLen == 24 {
			return Response{Result: recs("long-scope"), TTL: 5, Scope: 24}, nil
		}
		return Response{Result: recs("zero-scope"), TTL: 100, Scope: 0}, nil
	}}
	c := New(8, fc.now, up)

	// 写入 10.1.2.0/24（TTL 5）。
	q24 := Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 1, 2, 3), SourcePrefixLen: 24}
	if got := mustResolve(t, c, q24); got.Records[0] != "long-scope" {
		t.Fatalf("got %v", got)
	}
	// 用另一地址写入 /0（TTL 100），避免被 /24 条目拦截。
	q0other := Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 9, 9, 9), SourcePrefixLen: 0}
	if got := mustResolve(t, c, q0other); got.Records[0] != "zero-scope" {
		t.Fatalf("got %v", got)
	}
	// 未过期时：10.1.2.3 命中更长的 /24。
	if got := mustResolve(t, c, q24); got.Records[0] != "long-scope" || up.n() != 2 {
		t.Fatalf("should hit /24: got=%v calls=%d", got, up.n())
	}
	// /24 过期后：同一地址回落到 /0 条目，不再触发上游。
	fc.advance(5 * time.Second)
	if got := mustResolve(t, c, q24); got.Records[0] != "zero-scope" || up.n() != 2 {
		t.Fatalf("should fall back to /0: got=%v calls=%d", got, up.n())
	}
}

// 适用范围为零的条目覆盖该地址族所有地址。
func TestScopeZeroCoversAllAddresses(t *testing.T) {
	fc := newFakeClock()
	up := &recorder{fn: staticUpstream(Response{Result: recs("all"), TTL: 100, Scope: 0})}
	c := New(4, fc.now, up)

	mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v4(1, 2, 3, 4), SourcePrefixLen: 32})
	// 同族任意地址均命中，不再访问上游。
	for _, addr := range [][]byte{v4(9, 9, 9, 9), v4(255, 0, 0, 1), v4(1, 2, 3, 4)} {
		got := mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: addr, SourcePrefixLen: 0})
		if got.Records[0] != "all" {
			t.Fatalf("addr %v: got %v", addr, got)
		}
	}
	if up.n() != 1 {
		t.Fatalf("upstream calls = %d, want 1", up.n())
	}
}

// 适用范围大于源前缀长度时按源前缀长度缓存（钳制）。
func TestScopeLongerThanSourcePrefixIsClamped(t *testing.T) {
	fc := newFakeClock()
	up := &recorder{fn: staticUpstream(Response{Result: recs("clamped"), TTL: 100, Scope: 24})}
	c := New(4, fc.now, up)

	// 源前缀 /8，上游声明 /24 → 按 /8 缓存。
	mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 1, 2, 3), SourcePrefixLen: 8})
	// 同一 /8 内其他地址命中。
	got := mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 7, 7, 7), SourcePrefixLen: 8})
	if got.Records[0] != "clamped" || up.n() != 1 {
		t.Fatalf("same /8 should hit: got=%v calls=%d", got, up.n())
	}
	// 不同 /8 未命中，再次访问上游。
	mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v4(11, 0, 0, 1), SourcePrefixLen: 8})
	if up.n() != 2 {
		t.Fatalf("different /8 should miss: calls=%d", up.n())
	}
}

// 适用范围小于源前缀长度时按适用范围缓存。
func TestScopeShorterThanSourcePrefix(t *testing.T) {
	fc := newFakeClock()
	up := &recorder{fn: staticUpstream(Response{Result: recs("s16"), TTL: 100, Scope: 16})}
	c := New(4, fc.now, up)

	mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 1, 2, 3), SourcePrefixLen: 24})
	// 同 /16 不同 /24 的地址命中。
	got := mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 1, 9, 9), SourcePrefixLen: 24})
	if got.Records[0] != "s16" || up.n() != 1 {
		t.Fatalf("same /16 should hit: got=%v calls=%d", got, up.n())
	}
	// 不同 /16 未命中。
	mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 2, 0, 1), SourcePrefixLen: 24})
	if up.n() != 2 {
		t.Fatalf("different /16 should miss: calls=%d", up.n())
	}
}

// 不同地址族的条目互不相干。
func TestAddressFamilyIsolation(t *testing.T) {
	fc := newFakeClock()
	up := &recorder{fn: func(_ context.Context, req Request) (Response, error) {
		if len(req.ClientAddress) == FamilyIPv4 {
			return Response{Result: recs("v4"), TTL: 100, Scope: 0}, nil
		}
		return Response{Result: recs("v6"), TTL: 100, Scope: 0}, nil
	}}
	c := New(4, fc.now, up)

	if got := mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v4(1, 2, 3, 4), SourcePrefixLen: 0}); got.Records[0] != "v4" {
		t.Fatalf("got %v", got)
	}
	// v4 的 /0 条目不覆盖 v6 地址。
	if got := mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v6(0x20, 0x01), SourcePrefixLen: 0}); got.Records[0] != "v6" {
		t.Fatalf("got %v", got)
	}
	if up.n() != 2 {
		t.Fatalf("calls = %d, want 2", up.n())
	}
	// 各自族内命中，不再访问上游。
	mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v4(8, 8, 8, 8), SourcePrefixLen: 0})
	mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v6(0xfe, 0x80), SourcePrefixLen: 0})
	if up.n() != 2 {
		t.Fatalf("calls = %d, want 2", up.n())
	}
}

// 否定结果与正常结果在同一键下相互覆盖、不并存（经查询路径，借过期触发重写）。
func TestNegativeAndPositiveOverwriteViaQuery(t *testing.T) {
	fc := newFakeClock()
	var step int32
	up := &recorder{fn: func(_ context.Context, _ Request) (Response, error) {
		if atomic.AddInt32(&step, 1) == 1 {
			return Response{Result: Result{Kind: KindNXDomain}, TTL: 10, Scope: 16}, nil
		}
		return Response{Result: recs("now-positive"), TTL: 10, Scope: 16}, nil
	}}
	c := New(4, fc.now, up)
	q := Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 1, 2, 3), SourcePrefixLen: 16}

	if got := mustResolve(t, c, q); got.Kind != KindNXDomain {
		t.Fatalf("want nxdomain, got %v", got)
	}
	if got := mustResolve(t, c, q); got.Kind != KindNXDomain || up.n() != 1 {
		t.Fatalf("negative result should be cached: got=%v calls=%d", got, up.n())
	}
	fc.advance(11 * time.Second) // 过期后重写为正常结果
	if got := mustResolve(t, c, q); got.Kind != KindRecords || got.Records[0] != "now-positive" {
		t.Fatalf("got %v", got)
	}
	// 旧否定结果不并存：桶内只剩一个条目。
	b := c.buckets[bucketKey{name: "a.com", typ: 1}]
	if len(b.entries) != 1 {
		t.Fatalf("bucket entries = %d, want 1", len(b.entries))
	}
	if got := mustResolve(t, c, q); got.Kind != KindRecords || up.n() != 2 {
		t.Fatalf("got=%v calls=%d", got, up.n())
	}
}

// 同键写入直接覆盖（白盒）：否定与正常结果不并存，且重新计时。
func TestStoreOverwriteSameKey(t *testing.T) {
	fc := newFakeClock()
	c := New(4, fc.now, &recorder{fn: staticUpstream(Response{Result: recs("x"), TTL: 1, Scope: 0})})
	now := fc.now()
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 1, 2, 3), 16, Result{Kind: KindNXDomain}, 100)
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 1, 9, 9), 16, recs("pos"), 50)
	b := c.buckets[bucketKey{name: "a.com", typ: 1}]
	if len(b.entries) != 1 {
		t.Fatalf("entries = %d, want 1 (overwrite)", len(b.entries))
	}
	e := b.lookup(FamilyIPv4, v4(10, 1, 2, 3), now)
	if e == nil || e.result.Kind != KindRecords {
		t.Fatalf("entry = %+v, want records", e)
	}
	// 重新计时：过期时刻为第二次写入 +50s。
	if want := now.Add(50 * time.Second); !e.expiresAt.Equal(want) {
		t.Fatalf("expiresAt = %v, want %v", e.expiresAt, want)
	}
	// 反向：正常结果被否定结果覆盖。
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 1, 0, 1), 16, Result{Kind: KindNoData}, 30)
	if len(b.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(b.entries))
	}
	if e := b.lookup(FamilyIPv4, v4(10, 1, 2, 3), now); e == nil || e.result.Kind != KindNoData {
		t.Fatalf("entry = %+v, want nodata", e)
	}
}

// 容量淘汰：超过 K 时淘汰剩余有效期最短的条目。
func TestEvictionShortestRemainingTTL(t *testing.T) {
	fc := newFakeClock()
	c := New(2, fc.now, &recorder{fn: staticUpstream(Response{Result: recs("x"), TTL: 1, Scope: 0})})
	now := fc.now()
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 1, 0, 0), 16, recs("A"), 100)
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 2, 0, 0), 16, recs("B"), 50)
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 3, 0, 0), 16, recs("C"), 200)
	// B 剩余有效期最短，应被淘汰。
	if e := b(c, "a.com").lookup(FamilyIPv4, v4(10, 2, 0, 1), now); e != nil {
		t.Fatalf("B should be evicted")
	}
	if e := b(c, "a.com").lookup(FamilyIPv4, v4(10, 1, 0, 1), now); e == nil || e.result.Records[0] != "A" {
		t.Fatalf("A should survive")
	}
	if e := b(c, "a.com").lookup(FamilyIPv4, v4(10, 3, 0, 1), now); e == nil || e.result.Records[0] != "C" {
		t.Fatalf("C should survive")
	}
}

// 淘汰并列时（剩余有效期相同）淘汰适用范围更长者。
func TestEvictionTieBreakLongerScope(t *testing.T) {
	fc := newFakeClock()
	c := New(2, fc.now, &recorder{fn: staticUpstream(Response{Result: recs("x"), TTL: 1, Scope: 0})})
	now := fc.now()
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 1, 0, 0), 16, recs("wide"), 100)
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 2, 3, 0), 24, recs("narrow"), 100)
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 3, 0, 0), 16, recs("new"), 100)
	if e := b(c, "a.com").lookup(FamilyIPv4, v4(10, 2, 3, 9), now); e != nil {
		t.Fatalf("longer scope (/24) should be evicted first")
	}
	if e := b(c, "a.com").lookup(FamilyIPv4, v4(10, 1, 0, 9), now); e == nil {
		t.Fatalf("/16 entry should survive the tie")
	}
}

// 淘汰再并列时（剩余有效期与范围均相同）淘汰更早写入者。
func TestEvictionTieBreakEarlierWrite(t *testing.T) {
	fc := newFakeClock()
	c := New(2, fc.now, &recorder{fn: staticUpstream(Response{Result: recs("x"), TTL: 1, Scope: 0})})
	now := fc.now()
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 1, 0, 0), 16, recs("old"), 100)
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 2, 0, 0), 16, recs("mid"), 100)
	c.storeLocked(now, "a.com", 1, FamilyIPv4, v4(10, 3, 0, 0), 16, recs("new"), 100)
	if e := b(c, "a.com").lookup(FamilyIPv4, v4(10, 1, 0, 9), now); e != nil {
		t.Fatalf("earlier written entry should be evicted first")
	}
	if e := b(c, "a.com").lookup(FamilyIPv4, v4(10, 2, 0, 9), now); e == nil {
		t.Fatalf("later written entry should survive")
	}
}

// 过期条目在淘汰前先行清除，不占用容量。
func TestExpiredEntriesDoNotConsumeCapacity(t *testing.T) {
	fc := newFakeClock()
	c := New(1, fc.now, &recorder{fn: staticUpstream(Response{Result: recs("x"), TTL: 1, Scope: 0})})
	t0 := fc.now()
	c.storeLocked(t0, "a.com", 1, FamilyIPv4, v4(10, 1, 0, 0), 16, recs("dead"), 5)
	fc.advance(10 * time.Second) // 已过期
	c.storeLocked(fc.now(), "a.com", 1, FamilyIPv4, v4(10, 2, 0, 0), 16, recs("alive"), 100)
	if e := b(c, "a.com").lookup(FamilyIPv4, v4(10, 2, 0, 9), fc.now()); e == nil {
		t.Fatalf("new entry should not be evicted by an expired one")
	}
}

func b(c *Cache, name string) *bucket { return c.buckets[bucketKey{name: name, typ: 1}] }

// 名字比较不区分大小写，并去掉一个末尾的点。
func TestNameNormalization(t *testing.T) {
	fc := newFakeClock()
	up := &recorder{fn: staticUpstream(Response{Result: recs("n"), TTL: 100, Scope: 0})}
	c := New(4, fc.now, up)

	mustResolve(t, c, Query{Name: "Example.COM.", Type: 1, ClientAddress: v4(1, 1, 1, 1), SourcePrefixLen: 0})
	for _, name := range []string{"example.com", "EXAMPLE.com", "example.COM."} {
		mustResolve(t, c, Query{Name: name, Type: 1, ClientAddress: v4(2, 2, 2, 2), SourcePrefixLen: 0})
	}
	if up.n() != 1 {
		t.Fatalf("calls = %d, want 1 (names must compare equal)", up.n())
	}
	// 发往上游的名字已规范化。
	if up.calls[0].Name != "example.com" {
		t.Fatalf("upstream name = %q", up.calls[0].Name)
	}
}

// 前缀长度之外的低位在使用前统一清零：带垃圾低位的地址与清零地址行为一致。
func TestPrefixLowBitsAreMasked(t *testing.T) {
	fc := newFakeClock()
	up := &recorder{fn: staticUpstream(Response{Result: recs("m"), TTL: 100, Scope: 8})}
	c := New(4, fc.now, up)

	mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 255, 255, 255), SourcePrefixLen: 8})
	got := mustResolve(t, c, Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 0, 0, 0), SourcePrefixLen: 8})
	if got.Records[0] != "m" || up.n() != 1 {
		t.Fatalf("masked prefixes must coincide: got=%v calls=%d", got, up.n())
	}
}

// 生存时间为零的应答不缓存。
func TestTTLZeroIsNotCached(t *testing.T) {
	fc := newFakeClock()
	up := &recorder{fn: staticUpstream(Response{Result: recs("z"), TTL: 0, Scope: 0})}
	c := New(4, fc.now, up)
	q := Query{Name: "a.com", Type: 1, ClientAddress: v4(1, 1, 1, 1), SourcePrefixLen: 0}
	mustResolve(t, c, q)
	mustResolve(t, c, q)
	mustResolve(t, c, q)
	if up.n() != 3 {
		t.Fatalf("calls = %d, want 3 (TTL=0 never cached)", up.n())
	}
}

// TTL 上限边界：604800 合法，604801 与负值按上游失败处理。
func TestTTLBounds(t *testing.T) {
	fc := newFakeClock()
	up := &recorder{fn: staticUpstream(Response{Result: recs("max"), TTL: MaxTTLSeconds, Scope: 0})}
	c := New(4, fc.now, up)
	q := Query{Name: "a.com", Type: 1, ClientAddress: v4(1, 1, 1, 1), SourcePrefixLen: 0}
	if got := mustResolve(t, c, q); got.Records[0] != "max" {
		t.Fatalf("got %v", got)
	}
	fc.advance(time.Duration(MaxTTLSeconds-1) * time.Second)
	mustResolve(t, c, q) // 仍命中
	if up.n() != 1 {
		t.Fatalf("calls = %d, want 1", up.n())
	}
	fc.advance(time.Second) // 恰到期
	mustResolve(t, c, q)
	if up.n() != 2 {
		t.Fatalf("calls = %d, want 2", up.n())
	}
}

// 参数非法的查询被拒绝，且不改变缓存；错误优先级：参数非法 > 上游失败。
func TestInvalidArgumentRejectedFirst(t *testing.T) {
	fc := newFakeClock()
	up := &recorder{fn: func(context.Context, Request) (Response, error) {
		return Response{}, errors.New("upstream is down")
	}}
	c := New(4, fc.now, up)

	bad := []Query{
		{Name: "a.com", Type: 1, ClientAddress: v4(1, 2, 3, 4), SourcePrefixLen: 33}, // 超上限
		{Name: "a.com", Type: 1, ClientAddress: v4(1, 2, 3, 4), SourcePrefixLen: -1}, // 负值
		{Name: "a.com", Type: 1, ClientAddress: v6(), SourcePrefixLen: 129},          // v6 超上限
		{Name: "a.com", Type: 1, ClientAddress: []byte{1, 2, 3}, SourcePrefixLen: 0}, // 非法地址族
		{Name: "", Type: 1, ClientAddress: v4(1, 2, 3, 4), SourcePrefixLen: 0},       // 空名字
	}
	for _, q := range bad {
		_, err := c.Resolve(context.Background(), q)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("query %+v: err = %v, want ErrInvalidArgument", q, err)
		}
		if errors.Is(err, ErrUpstream) {
			t.Fatalf("query %+v: ErrInvalidArgument must take priority over ErrUpstream", q)
		}
	}
	if up.n() != 0 {
		t.Fatalf("rejected queries must not reach upstream, calls = %d", up.n())
	}
	if len(c.buckets) != 0 {
		t.Fatalf("rejected queries must not change the cache")
	}
}

// 上游失败：报错且不写缓存；并发等待者全部收到上游失败。
func TestUpstreamFailure(t *testing.T) {
	fc := newFakeClock()
	var fail int32 = 1
	up := &recorder{fn: func(context.Context, Request) (Response, error) {
		if atomic.LoadInt32(&fail) == 1 {
			return Response{}, errors.New("boom")
		}
		return Response{Result: recs("ok"), TTL: 100, Scope: 0}, nil
	}}
	c := New(4, fc.now, up)
	q := Query{Name: "a.com", Type: 1, ClientAddress: v4(1, 2, 3, 4), SourcePrefixLen: 24}

	if _, err := c.Resolve(context.Background(), q); !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	if len(c.buckets) != 0 {
		t.Fatalf("failed query must not populate the cache")
	}
	atomic.StoreInt32(&fail, 0)
	if got := mustResolve(t, c, q); got.Records[0] != "ok" {
		t.Fatalf("retry after failure: got %v", got)
	}
}

// 上游应答非法（TTL/范围越界、类别非法）按上游失败处理且不写缓存。
func TestInvalidUpstreamResponse(t *testing.T) {
	cases := []Response{
		{Result: recs("x"), TTL: -1, Scope: 0},
		{Result: recs("x"), TTL: MaxTTLSeconds + 1, Scope: 0},
		{Result: recs("x"), TTL: 10, Scope: -1},
		{Result: recs("x"), TTL: 10, Scope: 33}, // v4 上限 32
		{Result: Result{Kind: Kind(99)}, TTL: 10, Scope: 0},
	}
	for i, resp := range cases {
		fc := newFakeClock()
		up := &recorder{fn: staticUpstream(resp)}
		c := New(4, fc.now, up)
		q := Query{Name: "a.com", Type: 1, ClientAddress: v4(1, 2, 3, 4), SourcePrefixLen: 24}
		if _, err := c.Resolve(context.Background(), q); !errors.Is(err, ErrUpstream) {
			t.Fatalf("case %d: err = %v, want ErrUpstream", i, err)
		}
		if len(c.buckets) != 0 {
			t.Fatalf("case %d: invalid response must not be cached", i)
		}
	}
}

// ---------- 并发测试 ----------

// 同源前缀的并发未命中只向上游发出一次，所有等待者共享结果。
func TestConcurrentMergeSingleUpstreamCall(t *testing.T) {
	release := make(chan struct{})
	up := &recorder{fn: func(context.Context, Request) (Response, error) {
		<-release // 阻塞，直到所有等待者都已加入合并组
		return Response{Result: recs("merged"), TTL: 300, Scope: 8}, nil
	}}
	c := New(4, time.Now, up)

	const n = 8
	var wg sync.WaitGroup
	results := make([]Result, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// 同一源前缀 10.0.0.0/8，不同完整地址。
			q := Query{Name: "a.com", Type: 1,
				ClientAddress: v4(10, byte(i), byte(i), byte(i)), SourcePrefixLen: 8}
			results[i], errs[i] = c.Resolve(context.Background(), q)
		}(i)
	}
	time.Sleep(150 * time.Millisecond)
	close(release)
	wg.Wait()

	if up.n() != 1 {
		t.Fatalf("upstream calls = %d, want 1 (merged)", up.n())
	}
	for i := range results {
		if errs[i] != nil {
			t.Fatalf("waiter %d: err = %v", i, errs[i])
		}
		if !reflect.DeepEqual(results[i], recs("merged")) {
			t.Fatalf("waiter %d: got %v", i, results[i])
		}
	}
}

// 声明范围比源前缀更长时，落在范围之外的等待者须独立重新查询（TTL=0，
// 结果不可缓存，重新查询直达上游）。
func TestConcurrentNonCoveredWaiterRequeries(t *testing.T) {
	release := make(chan struct{})
	var call int32
	up := &recorder{fn: func(_ context.Context, req Request) (Response, error) {
		n := atomic.AddInt32(&call, 1)
		if n == 1 {
			<-release // 等待两个查询都进入合并组
			// 声明 /16：只覆盖代表地址 10.1.x.x，不覆盖 10.2.x.x。
			return Response{Result: recs("first"), TTL: 0, Scope: 16}, nil
		}
		// 未覆盖者的独立重新查询：声明 /8，覆盖其地址。
		return Response{Result: recs("second"), TTL: 0, Scope: 8}, nil
	}}
	c := New(4, time.Now, up)

	// 先发起的查询成为代表（地址 10.1.0.1）。
	type outcome struct {
		res Result
		err error
	}
	ch1 := make(chan outcome, 1)
	go func() {
		r, e := c.Resolve(context.Background(),
			Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 1, 0, 1), SourcePrefixLen: 8})
		ch1 <- outcome{r, e}
	}()
	time.Sleep(50 * time.Millisecond) // 确保第一个查询已成为代表
	ch2 := make(chan outcome, 1)
	go func() {
		r, e := c.Resolve(context.Background(),
			Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 2, 0, 2), SourcePrefixLen: 8})
		ch2 <- outcome{r, e}
	}()
	time.Sleep(50 * time.Millisecond) // 确保第二个查询已加入合并组
	close(release)

	o1, o2 := <-ch1, <-ch2
	if o1.err != nil || o2.err != nil {
		t.Fatalf("errs: %v %v", o1.err, o2.err)
	}
	if o1.res.Records[0] != "first" {
		t.Fatalf("covered waiter got %v, want first", o1.res)
	}
	if o2.res.Records[0] != "second" {
		t.Fatalf("non-covered waiter got %v, want second (independent re-query)", o2.res)
	}
	if up.n() != 2 {
		t.Fatalf("upstream calls = %d, want 2", up.n())
	}
}

// 声明范围比源前缀更长且结果可缓存（TTL>0）时：按源前缀长度钳制缓存，
// 未覆盖的等待者重新查询时命中该缓存条目（与串行执行等价）。
func TestConcurrentNonCoveredWaiterHitsClampedEntry(t *testing.T) {
	release := make(chan struct{})
	up := &recorder{fn: func(context.Context, Request) (Response, error) {
		<-release
		return Response{Result: recs("shared"), TTL: 300, Scope: 16}, nil
	}}
	c := New(4, time.Now, up)

	done1 := make(chan Result, 1)
	go func() {
		r, _ := c.Resolve(context.Background(),
			Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 1, 0, 1), SourcePrefixLen: 8})
		done1 <- r
	}()
	time.Sleep(50 * time.Millisecond)
	done2 := make(chan Result, 1)
	go func() {
		r, _ := c.Resolve(context.Background(),
			Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 2, 0, 2), SourcePrefixLen: 8})
		done2 <- r
	}()
	time.Sleep(50 * time.Millisecond)
	close(release)

	r1, r2 := <-done1, <-done2
	if r1.Records[0] != "shared" || r2.Records[0] != "shared" {
		t.Fatalf("got %v / %v, want shared/shared", r1, r2)
	}
	if up.n() != 1 {
		t.Fatalf("upstream calls = %d, want 1 (re-query served by clamped cache entry)", up.n())
	}
}

// 上游失败时所有并发等待者都报上游失败，且不写入缓存。
func TestConcurrentUpstreamFailureSharedByAllWaiters(t *testing.T) {
	release := make(chan struct{})
	up := &recorder{fn: func(context.Context, Request) (Response, error) {
		<-release
		return Response{}, errors.New("boom")
	}}
	c := New(4, time.Now, up)

	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.Resolve(context.Background(),
				Query{Name: "a.com", Type: 1, ClientAddress: v4(10, 0, 0, byte(i)), SourcePrefixLen: 8})
		}(i)
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()

	if up.n() != 1 {
		t.Fatalf("upstream calls = %d, want 1", up.n())
	}
	for i := range errs {
		if !errors.Is(errs[i], ErrUpstream) {
			t.Fatalf("waiter %d: err = %v, want ErrUpstream", i, errs[i])
		}
	}
	if len(c.buckets) != 0 {
		t.Fatalf("failed upstream query must not populate the cache")
	}
}

// 并发调用的结果须等价于某个串行顺序：构造一个所有串行顺序结果一致的
// 场景（上游结果只取决于名字与类型），并发执行后每个查询都必须得到该结果。
func TestConcurrentSerialEquivalence(t *testing.T) {
	names := []string{"a.com", "b.com"}
	types := []uint16{1, 28}
	want := map[string]Result{}
	for _, n := range names {
		for _, ty := range types {
			want[fmt.Sprintf("%s/%d", n, ty)] = recs(fmt.Sprintf("rec-%s-%d", n, ty))
		}
	}
	var calls int32
	up := &recorder{fn: func(_ context.Context, req Request) (Response, error) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(time.Millisecond) // 放大并发交错窗口
		// 结果只取决于 (name, type)；范围在 0..32 间随机但确定。
		scope := int(req.Name[0]+byte(req.Type)) % 33
		return Response{Result: want[fmt.Sprintf("%s/%d", req.Name, req.Type)], TTL: 600, Scope: scope}, nil
	}}
	c := New(1024, time.Now, up) // 足够大的 K，排除淘汰对本断言的干扰

	const goroutines = 64
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	got := make([]Result, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := names[i%len(names)]
			ty := types[(i/len(names))%len(types)]
			q := Query{Name: name, Type: ty,
				ClientAddress: v4(10, byte(i>>8), byte(i), 1), SourcePrefixLen: (i % 5) * 8}
			got[i], errs[i] = c.Resolve(context.Background(), q)
		}(i)
	}
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: err = %v", i, errs[i])
		}
		name := names[i%len(names)]
		ty := types[(i/len(names))%len(types)]
		if w := want[fmt.Sprintf("%s/%d", name, ty)]; !reflect.DeepEqual(got[i], w) {
			t.Fatalf("goroutine %d: got %v, want %v", i, got[i], w)
		}
	}
	// 上游调用次数不超过该场景下不同的合并组数量（同名字、同类型、同源前缀）。
	groups := map[string]struct{}{}
	for i := 0; i < goroutines; i++ {
		name := names[i%len(names)]
		ty := types[(i/len(names))%len(types)]
		srcLen := (i % 5) * 8
		prefix := maskAddr(v4(10, byte(i>>8), byte(i), 1), srcLen)
		groups[fmt.Sprintf("%s/%d/%d/%v", name, ty, srcLen, prefix)] = struct{}{}
	}
	if int(calls) > len(groups) {
		t.Fatalf("upstream calls = %d, exceeds distinct group count %d", calls, len(groups))
	}
	if int(calls) >= goroutines {
		t.Fatalf("upstream calls = %d, want merging across %d queries", calls, goroutines)
	}
	t.Logf("upstream calls = %d for %d queries across %d distinct groups", calls, goroutines, len(groups))
}
