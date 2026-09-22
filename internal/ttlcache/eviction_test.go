package ttlcache

import "testing"

// 容量满时优先驱逐已过期项，即使它不是最久未使用的。
func TestEvictExpiredFirst(t *testing.T) {
	c, clk := newCache(t, 2)
	mustPut(t, c, "a", "1", 10)  // t0=0, 过期点=10
	mustPut(t, c, "b", "2", 100) // t0=0, 过期点=100

	clk.Advance(15) // 时刻 15：a 已过期，b 有效
	mustPut(t, c, "c", "3", 100)

	if _, ok := c.Get("a"); ok {
		t.Error("a 应被驱逐")
	}
	if val, ok := c.Get("b"); !ok || val != "2" {
		t.Errorf("b 应保留, Get(b) = %q,%v", val, ok)
	}
	if val, ok := c.Get("c"); !ok || val != "3" {
		t.Errorf("c 应存在, Get(c) = %q,%v", val, ok)
	}
	if c.Len() != 2 {
		t.Errorf("Len() = %d, 期望 2", c.Len())
	}
}

// 没有过期项时驱逐最久未使用的项。
func TestEvictLRUWhenNoneExpired(t *testing.T) {
	c, _ := newCache(t, 2)
	mustPut(t, c, "a", "1", 100)
	mustPut(t, c, "b", "2", 100)

	if _, ok := c.Get("a"); !ok { // a 提升为最近使用，b 成为最久未使用
		t.Fatal("Get(a) 应命中")
	}
	mustPut(t, c, "c", "3", 100)

	if _, ok := c.Get("b"); ok {
		t.Error("b 应被驱逐（最久未使用）")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("a 应保留")
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("c 应存在")
	}
}

// 多个过期项时驱逐写入时刻最早者，与谁最近被访问无关。
func TestEvictEarliestWrittenExpired(t *testing.T) {
	c, clk := newCache(t, 2)
	mustPut(t, c, "a", "1", 10) // t0=0, 过期点=10
	clk.Advance(1)
	mustPut(t, c, "b", "2", 10) // t0=1, 过期点=11

	clk.Advance(4) // 时刻 5：a 仍有效，Get 把它提升为最近使用
	if _, ok := c.Get("a"); !ok {
		t.Fatal("时刻 5 Get(a) 应命中")
	}

	clk.Advance(6) // 时刻 11：a、b 均已过期，a 是最近使用者
	mustPut(t, c, "c", "3", 100)

	// a 写入时刻最早，应被驱逐（尽管它最近被访问）；
	// b 虽过期但未被清理，仍占容量。
	if c.Len() != 2 {
		t.Fatalf("Len() = %d, 期望 2（b 过期但未清理）", c.Len())
	}
	if !c.Delete("b") {
		t.Error("b 应仍在缓存中（过期但未清理）")
	}
	if c.Delete("a") {
		t.Error("a 应已被驱逐")
	}
}

// 更新已存在的键不触发驱逐，也不改变容量占用。
func TestPutExistingKeyNoEviction(t *testing.T) {
	c, _ := newCache(t, 2)
	mustPut(t, c, "a", "1", 100)
	mustPut(t, c, "b", "2", 100)
	mustPut(t, c, "a", "1-new", 100)

	if c.Len() != 2 {
		t.Errorf("Len() = %d, 期望 2", c.Len())
	}
	if val, ok := c.Get("a"); !ok || val != "1-new" {
		t.Errorf("Get(a) = %q,%v, 期望 \"1-new\",true", val, ok)
	}
	if _, ok := c.Get("b"); !ok {
		t.Error("b 不应被驱逐")
	}
}

// 更新已存在的键会把它提升为最近使用。
func TestPutExistingKeyPromotes(t *testing.T) {
	c, _ := newCache(t, 2)
	mustPut(t, c, "a", "1", 100)
	mustPut(t, c, "b", "2", 100)
	mustPut(t, c, "a", "1-new", 100) // a 提升为最近使用，b 成为最久未使用
	mustPut(t, c, "c", "3", 100)

	if _, ok := c.Get("b"); ok {
		t.Error("b 应被驱逐")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("a 应保留")
	}
}
