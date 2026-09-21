package ttlcache

import "testing"

// 到点即过期：t0+d 已过期，t0+d-1 仍有效。
func TestExpiryBoundary(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "1", 10) // 写入于 t0=0，过期时刻为 10

	clk.Advance(9) // t0+d-1
	if _, ok := c.Get("a"); !ok {
		t.Error("Get(a) at t0+d-1 = miss, want hit")
	}

	clk.Advance(1) // t0+d
	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) at t0+d = hit, want miss")
	}
}

// Get 命中过期项：返回 miss 并立即删除，Len 随之减一。
func TestGetExpiredRemovesEntry(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "1", 10)

	clk.Advance(10)
	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) expired = hit, want miss")
	}
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0 after expired Get", c.Len())
	}
}

// 删除一个已过期但尚未清理的项，算删掉了，返回 true。
func TestDeleteExpiredReturnsTrue(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "1", 10)

	clk.Advance(10)
	if !c.Delete("a") {
		t.Error("Delete(expired a) = false, want true")
	}
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0", c.Len())
	}
}

// 重复 Put 已存在的键：更新值、提升为最近使用，但不刷新 TTL。
func TestPutExistingDoesNotRefreshTTL(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "old", 10) // t0=0，过期时刻 10

	clk.Advance(5)
	mustPut(t, c, "a", "new", 100) // 只更新值，TTL 仍以首次写入为准

	clk.Advance(4) // 时刻 9
	if val, ok := c.Get("a"); !ok || val != "new" {
		t.Errorf("Get(a) at 9 = %q, %v; want %q, true", val, ok, "new")
	}

	clk.Advance(1) // 时刻 10：按首次写入的 TTL 已过期
	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) at 10 = hit, want miss (TTL not refreshed)")
	}
}

// 重复 Put 已存在的键要把它提升为最近使用。
func TestPutExistingPromotesRecency(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "1", 1000)
	mustPut(t, c, "b", "2", 1000)

	mustPut(t, c, "a", "1'", 1000) // a 变为最近使用
	mustPut(t, c, "c", "3", 1000)  // 无过期项，驱逐最久未使用的 b

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

// Get 命中未过期项时提升为最近使用。
func TestGetPromotesRecency(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "1", 1000)
	mustPut(t, c, "b", "2", 1000)

	if _, ok := c.Get("a"); !ok {
		t.Fatal("Get(a) = miss, want hit")
	}
	mustPut(t, c, "c", "3", 1000) // 应驱逐 b 而非 a

	if _, ok := c.Get("b"); ok {
		t.Error("Get(b) = hit, want evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("Get(a) = miss, want hit")
	}
}
