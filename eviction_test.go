package ontology

import (
	"testing"
	"time"
)

// 超出 K 时淘汰剩余有效期最短者；过期条目先被惰性清除，不参与淘汰。
func TestEvictionShortestRemainingFirst(t *testing.T) {
	clk := newFakeClock()
	// 用不同的 /24 子网作为互不相干的键；第三个八位组同时编码 TTL。
	c := NewCache(2, clk, UpstreamFunc(func(q Query) (Answer, error) {
		ttl := int(q.Client[2])
		return Answer{Kind: KindRecords, Records: []byte{q.Client[2]}, TTL: ttl, ScopePrefix: 24}, nil
	}))
	ask := func(subnet byte) {
		mustQuery(t, c, Query{Name: "e", Rrtype: 1, Family: FamilyV4,
			Client: v4(10, 0, subnet, 1), SrcPrefix: 24})
	}
	ask(10) // 10.0.10.0/24, ttl 10
	clk.advance(1 * time.Second)
	ask(20) // 10.0.20.0/24, ttl 20
	clk.advance(1 * time.Second)
	ask(30) // 触发淘汰：剩余 8 < 19 -> 淘汰 10.0.10.0/24

	snapshot := func() map[string]bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		out := map[string]bool{}
		for pk := range c.buckets[nameKey{"e", 1}].entries {
			out[pk.prefix] = true
		}
		return out
	}
	survive := snapshot()
	if survive[string(v4(10, 0, 10, 0))] ||
		!survive[string(v4(10, 0, 20, 0))] ||
		!survive[string(v4(10, 0, 30, 0))] {
		t.Fatalf("want .20/.24 and .30/.24 survive, got %v", survive)
	}

	// 距 t0 恰 20s：10.0.20 恰到期，应先被清除，再写入 10.0.40。
	clk.advance(18 * time.Second)
	ask(40)
	survive = snapshot()
	if survive[string(v4(10, 0, 20, 0))] {
		t.Fatalf("exact-deadline entry must be purged, got %v", survive)
	}
	if !survive[string(v4(10, 0, 30, 0))] || !survive[string(v4(10, 0, 40, 0))] {
		t.Fatalf("want .30/.24 and .40/.24 survive, got %v", survive)
	}
}

// deadline 并列时先淘汰范围更长者。
// 注意共存前提：更长前缀条目必须先存在，且更短范围的应答来自不被覆盖的客户端区域，
// 否则更短范围条目一旦写入，长前缀区域内的客户端将永远先命中它。因此按
// “由长到短、区域互不覆盖”的顺序构造 /24、/16、/8。
func TestEvictionTieLongerScopeFirst(t *testing.T) {
	clk := newFakeClock()
	c := NewCache(2, clk, UpstreamFunc(func(q Query) (Answer, error) {
		return Answer{Kind: KindRecords, Records: []byte{byte(q.SrcPrefix)}, TTL: 100, ScopePrefix: q.SrcPrefix}, nil
	}))
	// 1) /24 条目（10.2.2.0 区域）
	mustQuery(t, c, Query{Name: "tie", Rrtype: 1, Family: FamilyV4, Client: v4(10, 2, 2, 1), SrcPrefix: 24})
	// 2) /16 条目，来自 10.3 区域，不被 /24 覆盖；写入后两者同 deadline，
	//    并列淘汰更长范围 -> /24 出局，保留 /16。
	mustQuery(t, c, Query{Name: "tie", Rrtype: 1, Family: FamilyV4, Client: v4(10, 3, 3, 1), SrcPrefix: 16})
	// 3) /8 条目来自 10.9 区域，不被 /16 覆盖；同 deadline 并列，淘汰更长的 /16。
	mustQuery(t, c, Query{Name: "tie", Rrtype: 1, Family: FamilyV4, Client: v4(10, 9, 9, 1), SrcPrefix: 8})

	c.mu.Lock()
	got := map[int]bool{}
	for pk := range c.buckets[nameKey{"tie", 1}].entries {
		got[pk.bits] = true
	}
	c.mu.Unlock()
	if !got[8] || !got[16] || got[24] {
		t.Fatalf("equal deadline must evict longest coexisting scope /24, got %v", got)
	}
}

// 前两级均并列（同 deadline、同范围长度）时淘汰更早写入者。
func TestEvictionTieEarlierWriteFirst(t *testing.T) {
	// t0 连续写入两个同 TTL、同 /24、不同子网的条目；第三秒写入第三个，TTL 为 97，
	// 使三者 deadline 同为 t0+100，此时淘汰写入序号最小者（最早写入的 10.0.1.0/24）。
	clk := newFakeClock()
	c := NewCache(2, clk, UpstreamFunc(func(q Query) (Answer, error) {
		var ttl int
		switch q.Client[2] {
		case 1:
			ttl = 100
		case 2:
			ttl = 100
		default:
			ttl = 97
		}
		return Answer{Kind: KindRecords, Records: []byte{q.Client[2]}, TTL: ttl, ScopePrefix: 24}, nil
	}))
	mustQuery(t, c, Query{Name: "w", Rrtype: 1, Family: FamilyV4, Client: v4(10, 0, 1, 1), SrcPrefix: 24})
	mustQuery(t, c, Query{Name: "w", Rrtype: 1, Family: FamilyV4, Client: v4(10, 0, 2, 1), SrcPrefix: 24})
	clk.advance(3 * time.Second)
	mustQuery(t, c, Query{Name: "w", Rrtype: 1, Family: FamilyV4, Client: v4(10, 0, 3, 1), SrcPrefix: 24})

	c.mu.Lock()
	survive := map[string]bool{}
	for pk := range c.buckets[nameKey{"w", 1}].entries {
		survive[pk.prefix] = true
	}
	c.mu.Unlock()
	if survive[string(v4(10, 0, 1, 0))] ||
		!survive[string(v4(10, 0, 2, 0))] ||
		!survive[string(v4(10, 0, 3, 0))] {
		t.Fatalf("fully tied entries must evict earliest writer .1, got %v", survive)
	}
}

// 同键整体覆盖：重新计时且不新增条目。
func TestOverwriteResetsEntry(t *testing.T) {
	clk := newFakeClock()
	calls := 0
	c := NewCache(1, clk, UpstreamFunc(func(q Query) (Answer, error) {
		calls++
		return Answer{Kind: KindRecords, Records: []byte("x"), TTL: 5, ScopePrefix: 0}, nil
	}))
	q := Query{Name: "k", Rrtype: 1, Family: FamilyV4, Client: v4(9, 9, 9, 9)}
	mustQuery(t, c, q)
	clk.advance(6 * time.Second) // 恰过期后再查 -> 上游、整体覆盖同键
	mustQuery(t, c, q)
	if calls != 2 {
		t.Fatalf("want 2 upstream calls, got %d", calls)
	}
	c.mu.Lock()
	n := len(c.buckets[nameKey{"k", 1}].entries)
	e := c.buckets[nameKey{"k", 1}].entries[prefixKey{
		name: nameKey{"k", 1}, family: FamilyV4, prefix: string(v4(0, 0, 0, 0)), bits: 0,
	}]
	c.mu.Unlock()
	if n != 1 || e == nil {
		t.Fatalf("overwrite must keep exactly 1 entry, n=%d", n)
	}
}
