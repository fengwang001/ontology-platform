package ttlcache

import "testing"

// 场景一：容量满时存在过期项，优先驱逐过期项而非 LRU 项。
// 容量 2，a(TTL 10)、b(TTL 100)，时刻推进到 15 再写 c：驱逐 a，b 保留。
func TestEvictPrefersExpiredOverLRU(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "1", 10)
	mustPut(t, c, "b", "2", 100)

	clk.Advance(15) // a 已过期，b 未过期
	mustPut(t, c, "c", "3", 100)

	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) = hit, want evicted")
	}
	if val, ok := c.Get("b"); !ok || val != "2" {
		t.Errorf("Get(b) = %q, %v; want %q, true", val, ok, "2")
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("Get(c) = miss, want hit")
	}
	if c.Len() != 2 {
		t.Errorf("Len() = %d, want 2", c.Len())
	}
}

// 场景二：没有过期项时驱逐最久未使用项。
// 容量 2，a、b 均未过期，Get(a) 之后写 c：驱逐 b。
func TestEvictLRUWhenNothingExpired(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "1", 1000)
	mustPut(t, c, "b", "2", 1000)

	if _, ok := c.Get("a"); !ok { // a 提升为最近使用，b 成为最久未使用
		t.Fatal("Get(a) = miss, want hit")
	}
	mustPut(t, c, "c", "3", 1000)

	if _, ok := c.Get("b"); ok {
		t.Error("Get(b) = hit, want evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("Get(a) = miss, want hit")
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("Get(c) = miss, want hit")
	}
}

// 场景三：多个过期项时驱逐写入时刻最早者，与最近访问无关。
// 容量 2，a(t0=0)、b(t0=5) 均已过期；先访问 b 再写 c：仍驱逐 a。
func TestEvictEarliestWrittenExpired(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "1", 10) // 写入时刻 0

	clk.Advance(5)
	mustPut(t, c, "b", "2", 10) // 写入时刻 5

	clk.Advance(5)               // 时刻 10：a、b 均已过期
	mustPut(t, c, "c", "3", 100) // 触发驱逐：应驱逐写入最早的 a

	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) = hit, want evicted (earliest write)")
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("Get(b) = miss, want kept (later write)")
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("Get(c) = miss, want hit")
	}
}

// 过期项按写入时刻驱逐，而不是按访问顺序：
// 即使较早写入的过期项最近被 Get 过（Get 时它尚未过期），仍先驱逐它。
func TestEvictExpiredIgnoresRecency(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "1", 10) // 写入时刻 0

	clk.Advance(5)
	mustPut(t, c, "b", "2", 10) // 写入时刻 5

	clk.Advance(4) // 时刻 9：a、b 均未过期
	if _, ok := c.Get("a"); !ok {
		t.Fatal("Get(a) = miss, want hit")
	}

	clk.Advance(1) // 时刻 10：a、b 均过期，a 是最近访问但写入更早
	mustPut(t, c, "c", "3", 100)

	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) = hit, want evicted despite recent access")
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("Get(b) = miss, want kept")
	}
}

// 场景四：重复 Put 不刷新 TTL。
// 写 a(TTL 10)，时刻 5 重 Put 新值：时刻 9 拿到新值，时刻 10 拿不到。
func TestReputKeepsOriginalExpiry(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "old", 10)

	clk.Advance(5)
	mustPut(t, c, "a", "new", 10)

	clk.Advance(4) // 时刻 9
	if val, ok := c.Get("a"); !ok || val != "new" {
		t.Errorf("Get(a) at 9 = %q, %v; want %q, true", val, ok, "new")
	}

	clk.Advance(1) // 时刻 10
	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) at 10 = hit, want miss (TTL not refreshed)")
	}
}
