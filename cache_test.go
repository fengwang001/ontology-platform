package ontology

import (
	"bytes"
	"testing"
	"time"
)

// 参数非法：族/地址长度/前缀越界，且任何非法查询都不触发上游、不改缓存。
func TestInvalidArguments(t *testing.T) {
	clk := newFakeClock()
	up := newScriptedUpstream(nil)
	c := NewCache(10, clk, up)

	bad := []Query{
		{Name: "a.", Rrtype: 1, Family: FamilyUnspecified, Client: v4(1, 1, 1, 1), SrcPrefix: 0},
		{Name: "a.", Rrtype: 1, Family: FamilyV4, Client: v6h(1), SrcPrefix: 0},
		{Name: "a.", Rrtype: 1, Family: FamilyV6, Client: v4(1, 1, 1, 1), SrcPrefix: 0},
		{Name: "a.", Rrtype: 1, Family: FamilyV4, Client: v4(1, 1, 1, 1), SrcPrefix: 33},
		{Name: "a.", Rrtype: 1, Family: FamilyV4, Client: v4(1, 1, 1, 1), SrcPrefix: -1},
		{Name: "a.", Rrtype: 1, Family: FamilyV6, Client: v6h(1), SrcPrefix: 129},
		{Name: "a.", Rrtype: 1, Family: AddrFamily(99), Client: v4(1, 1, 1, 1), SrcPrefix: 0},
		{Name: "a.", Rrtype: 1, Family: FamilyV4, Client: Addr{1, 2, 3}, SrcPrefix: 0},
	}
	for _, q := range bad {
		wantErr(t, c, q, ErrInvalidArgument)
	}
	if up.calls != 0 {
		t.Fatalf("invalid queries triggered %d upstream calls", up.calls)
	}
}

// 拒绝次序：参数非法优先于上游失败——即使上游永远失败，非法参数仍报参数非法。
func TestErrorPriority(t *testing.T) {
	clk := newFakeClock()
	c := NewCache(10, clk, UpstreamFunc(func(Query) (Answer, error) {
		return Answer{}, errBoom
	}))
	// 合法查询 -> 上游失败
	wantErr(t, c, Query{Name: "a", Rrtype: 1, Family: FamilyV4, Client: v4(1, 1, 1, 1)}, ErrUpstreamFailure)
	// 非法查询 -> 参数非法（而非上游失败）
	wantErr(t, c, Query{Name: "a", Rrtype: 1, Family: FamilyV4, Client: v4(1, 1, 1, 1), SrcPrefix: 40}, ErrInvalidArgument)
}

// 生存时间左闭右开：到期前一刻命中，恰到期与之后未命中。
func TestTTLExpiryBoundary(t *testing.T) {
	clk := newFakeClock()
	up := newScriptedUpstream([]Answer{rec(10, 0), rec(20, 0)})
	c := NewCache(10, clk, up)
	q := Query{Name: "a.", Rrtype: 1, Family: FamilyV4, Client: v4(1, 2, 3, 4)}

	r := mustQuery(t, c, q)
	if r.Kind != KindRecords {
		t.Fatalf("kind = %v", r.Kind)
	}
	clk.advance(10*time.Second - time.Nanosecond)
	mustQuery(t, c, q) // 到期前一刻仍命中
	if up.calls != 1 {
		t.Fatalf("expected cache hit, upstream calls = %d", up.calls)
	}
	clk.advance(time.Nanosecond) // 恰到期
	mustQuery(t, c, q)
	if up.calls != 2 {
		t.Fatalf("expected miss at exact deadline, upstream calls = %d", up.calls)
	}
}

// 较长范围条目过期后，应回落到较短（含范围 0）的未过期条目。
func TestLongestPrefixFallbackAfterExpiry(t *testing.T) {
	clk := newFakeClock()
	up := newScriptedUpstream([]Answer{
		{Kind: KindRecords, Records: []byte("long"), TTL: 10, ScopePrefix: 24},
		{Kind: KindRecords, Records: []byte("wide"), TTL: 100, ScopePrefix: 0},
	})
	c := NewCache(10, clk, up)
	longClient := v4(10, 0, 0, 5)
	r := mustQuery(t, c, Query{Name: "a", Rrtype: 1, Family: FamilyV4, Client: longClient, SrcPrefix: 24}) // long /24
	if string(r.Records) != "long" {
		t.Fatalf("want long, got %s", r.Records)
	}
	// 另一个 /24 外的客户端取 /0 条目
	client := v4(10, 0, 1, 5)
	if r = mustQuery(t, c, Query{Name: "a", Rrtype: 1, Family: FamilyV4, Client: client}); string(r.Records) != "wide" {
		t.Fatalf("outside /24 want wide, got %s", r.Records)
	}
	// /24 内取最长 -> long
	if r = mustQuery(t, c, Query{Name: "a", Rrtype: 1, Family: FamilyV4, Client: longClient}); !bytes.Equal(r.Records, []byte("long")) {
		t.Fatalf("want longest-prefix long, got %s", r.Records)
	}
	// /24 到期后回落 wide
	clk.advance(11 * time.Second)
	if r = mustQuery(t, c, Query{Name: "a", Rrtype: 1, Family: FamilyV4, Client: longClient}); string(r.Records) != "wide" {
		t.Fatalf("after expiry want wide, got %s (upstream calls=%d)", r.Records, up.calls)
	}
	if up.calls != 2 {
		t.Fatalf("fallback should not hit upstream, calls=%d", up.calls)
	}
}

// 低位清零与大小写/末尾点规范化：不同表示共享同一条目。
func TestMaskingAndNameNormalization(t *testing.T) {
	clk := newFakeClock()
	up := newScriptedUpstream([]Answer{rec(30, 16), rec(30, 16)})
	c := NewCache(10, clk, up)
	r := mustQuery(t, c, Query{Name: "Example.COM.", Rrtype: 1, Family: FamilyV4, Client: v4(10, 1, 2, 3), SrcPrefix: 16})
	_ = r
	// 同一 /16 子网、同名字的其他大小写/带点写法应命中同一条目
	for _, name := range []string{"example.com", "EXAMPLE.com", "Example.Com.", "example.com."} {
		mustQuery(t, c, Query{Name: name, Rrtype: 1, Family: FamilyV4, Client: v4(10, 1, 99, 99)})
	}
	// 10.2.x.x 不在 /16 内应未命中
	mustQuery(t, c, Query{Name: "example.com", Rrtype: 1, Family: FamilyV4, Client: v4(10, 2, 0, 1)})
	if up.calls != 2 {
		t.Fatalf("expected 2 upstream calls (one /16 + one miss), got %d", up.calls)
	}
}

// 范围为零覆盖该族所有地址；上游范围大于源前缀时按源前缀缓存。
func TestZeroAndOverscopedAnswer(t *testing.T) {
	clk := newFakeClock()
	up := newScriptedUpstream([]Answer{
		{Kind: KindRecords, Records: []byte("zero"), TTL: 30, ScopePrefix: 0},
	})
	c := NewCache(10, clk, up)
	mustQuery(t, c, Query{Name: "z", Rrtype: 1, Family: FamilyV4, Client: v4(8, 8, 8, 8), SrcPrefix: 24})
	mustQuery(t, c, Query{Name: "z", Rrtype: 1, Family: FamilyV4, Client: v4(1, 2, 3, 4), SrcPrefix: 24})
	if up.calls != 1 {
		t.Fatalf("scope-0 answer should cover all v4, calls=%d", up.calls)
	}

	// 上游声明 /24，但查询只暴露 /16 -> 有效范围按 /16 缓存
	up2 := newScriptedUpstream([]Answer{
		{Kind: KindRecords, Records: []byte("capped"), TTL: 30, ScopePrefix: 24},
	})
	c2 := NewCache(10, clk, up2)
	mustQuery(t, c2, Query{Name: "o", Rrtype: 1, Family: FamilyV4, Client: v4(172, 16, 5, 9), SrcPrefix: 16})
	mustQuery(t, c2, Query{Name: "o", Rrtype: 1, Family: FamilyV4, Client: v4(172, 16, 9, 9), SrcPrefix: 16})
	if up2.calls != 1 {
		t.Fatalf("overscoped answer should be cached at /16, calls=%d", up2.calls)
	}
}

// 地址族隔离：v4/v6 同名字同类型互不相干。
func TestFamilyIsolation(t *testing.T) {
	clk := newFakeClock()
	up := newScriptedUpstream([]Answer{
		{Kind: KindRecords, Records: []byte("v4ans"), TTL: 30, ScopePrefix: 0},
		{Kind: KindRecords, Records: []byte("v6ans"), TTL: 30, ScopePrefix: 0},
	})
	c := NewCache(10, clk, up)
	mustQuery(t, c, Query{Name: "dual", Rrtype: 1, Family: FamilyV4, Client: v4(1, 1, 1, 1)})
	mustQuery(t, c, Query{Name: "dual", Rrtype: 1, Family: FamilyV6, Client: v6h(0x20, 0x01, 0x0d, 0xb8)})
	if up.calls != 2 {
		t.Fatalf("families must be isolated, calls=%d", up.calls)
	}
}

// 否定结果与正常结果互相覆盖（同键不并存），并照常命中与缓存。
func TestNegativeOverwritesAndViceVersa(t *testing.T) {
	clk := newFakeClock()
	up := newScriptedUpstream([]Answer{
		{Kind: KindRecords, Records: []byte("ok"), TTL: 30, ScopePrefix: 0},
		{Kind: KindNoName, TTL: 30, ScopePrefix: 0},
		{Kind: KindNoType, TTL: 30, ScopePrefix: 0},
		{Kind: KindRecords, Records: []byte("ok2"), TTL: 30, ScopePrefix: 0},
	})
	c := NewCache(10, clk, up)
	q := Query{Name: "n", Rrtype: 1, Family: FamilyV4, Client: v4(2, 2, 2, 2)}
	if r := mustQuery(t, c, q); r.Kind != KindRecords {
		t.Fatal("want records")
	}
	clk.advance(31 * time.Second)
	if r := mustQuery(t, c, q); r.Kind != KindNoName {
		t.Fatal("want nxdomain overwrite")
	}
	clk.advance(31 * time.Second)
	if r := mustQuery(t, c, q); r.Kind != KindNoType {
		t.Fatal("want nodata overwrite")
	}
	clk.advance(31 * time.Second)
	if r := mustQuery(t, c, q); r.Kind != KindRecords || string(r.Records) != "ok2" {
		t.Fatal("want records again")
	}
}

// TTL 为 0 的应答返回但不缓存。
func TestZeroTTLNotCached(t *testing.T) {
	clk := newFakeClock()
	up := newScriptedUpstream([]Answer{rec(0, 0), rec(0, 0)})
	c := NewCache(10, clk, up)
	q := Query{Name: "t0", Rrtype: 1, Family: FamilyV4, Client: v4(3, 3, 3, 3)}
	mustQuery(t, c, q)
	mustQuery(t, c, q)
	if up.calls != 2 {
		t.Fatalf("ttl=0 must not be cached, calls=%d", up.calls)
	}
}

// 非法上游应答（越界 TTL/范围、未知 kind）按上游失败处理且不写缓存。
func TestMalformedUpstreamAnswer(t *testing.T) {
	clk := newFakeClock()
	for _, bad := range []Answer{
		{Kind: Kind(9), TTL: 10, ScopePrefix: 0},
		{Kind: KindRecords, TTL: -1, ScopePrefix: 0},
		{Kind: KindRecords, TTL: maxTTLSeconds + 1, ScopePrefix: 0},
		{Kind: KindRecords, TTL: 10, ScopePrefix: 33},
	} {
		c := NewCache(10, clk, UpstreamFunc(func(Query) (Answer, error) { return bad, nil }))
		q := Query{Name: "m", Rrtype: 1, Family: FamilyV4, Client: v4(4, 4, 4, 4)}
		wantErr(t, c, q, ErrUpstreamFailure)
	}
}

var errBoom = boomErr{}

type boomErr struct{}

func (boomErr) Error() string { return "boom" }
