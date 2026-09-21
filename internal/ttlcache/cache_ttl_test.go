package ttlcache

import "testing"

// 到点即过期：t0+d 已过期，t0+d-1 仍有效。
func TestExpiryBoundary(t *testing.T) {
	clk := newClock(0)
	c := mustNew(4, clk)
	mustPut(c, "a", "1", 10) // 写入于 t=0，过期于 t=10

	clk.set(9)
	if v, ok := c.Get("a"); !ok || v != "1" {
		t.Fatalf("t=9: Get(a) = (%q,%v), want (1,true)", v, ok)
	}
	clk.set(10)
	if _, ok := c.Get("a"); ok {
		t.Fatal("t=10: Get(a) should miss (expired at exactly t0+ttl)")
	}
}

// Get 命中过期项时立即删除，Len 随之减一。
func TestGetExpiredRemovesEntry(t *testing.T) {
	clk := newClock(0)
	c := mustNew(4, clk)
	mustPut(c, "a", "1", 10)
	mustPut(c, "b", "2", 100)
	clk.set(15)

	if c.Len() != 2 {
		t.Fatalf("before access: Len = %d, want 2 (expired still counts)", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("Get(a) should miss")
	}
	if c.Len() != 1 {
		t.Fatalf("after expired Get: Len = %d, want 1", c.Len())
	}
	if c.Delete("a") {
		t.Fatal("expired entry already removed by Get, Delete should return false")
	}
	if v, ok := c.Get("b"); !ok || v != "2" {
		t.Fatalf("Get(b) = (%q,%v), want (2,true)", v, ok)
	}
}

// Put 已存在的键：更新值、不刷新 TTL。
func TestPutExistingDoesNotRefreshTTL(t *testing.T) {
	clk := newClock(0)
	c := mustNew(2, clk)
	mustPut(c, "a", "old", 10) // 写入于 t=0，过期于 t=10

	clk.set(5)
	mustPut(c, "a", "new", 10) // 更新值，TTL 仍以 t=0 为准

	clk.set(9)
	if v, ok := c.Get("a"); !ok || v != "new" {
		t.Fatalf("t=9: Get(a) = (%q,%v), want (new,true)", v, ok)
	}
	clk.set(10)
	if _, ok := c.Get("a"); ok {
		t.Fatal("t=10: Get(a) should miss, TTL must not be refreshed by update")
	}
}

// Put 已存在的键：提升为最近使用。
func TestPutExistingPromotesRecency(t *testing.T) {
	clk := newClock(0)
	c := mustNew(2, clk)
	mustPut(c, "a", "1", 100)
	mustPut(c, "b", "2", 100)
	mustPut(c, "a", "1v2", 100) // a 变最近使用
	mustPut(c, "c", "3", 100)   // 应驱逐 b

	if _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted")
	}
	if v, ok := c.Get("a"); !ok || v != "1v2" {
		t.Fatalf("Get(a) = (%q,%v), want (1v2,true)", v, ok)
	}
	if v, ok := c.Get("c"); !ok || v != "3" {
		t.Fatalf("Get(c) = (%q,%v), want (3,true)", v, ok)
	}
}

// 并发读写冒烟测试（配合 -race 使用）。
func TestConcurrentAccess(t *testing.T) {
	clk := newClock(0)
	c := mustNew(8, clk)
	done := make(chan struct{})
	for w := 0; w < 4; w++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			key := string(rune('a' + id))
			for i := 0; i < 200; i++ {
				_ = c.Put(key, "v", 1000)
				_, _ = c.Get(key)
				clk.advance(1)
			}
		}(w)
	}
	for w := 0; w < 4; w++ {
		<-done
	}
	if c.Len() > 8 {
		t.Fatalf("Len = %d exceeds capacity 8", c.Len())
	}
}
