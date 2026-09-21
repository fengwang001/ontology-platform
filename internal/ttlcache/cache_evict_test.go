package ttlcache

import "testing"

// 场景一：容量满且有过期项时，驱逐过期项而非 LRU 项。
// 容量 2，a(TTL 10)、b(TTL 100)，时刻 15 写 c：驱逐 a，保留 b。
func TestEvictPrefersExpired(t *testing.T) {
	clk := newClock(0)
	c := mustNew(2, clk)
	mustPut(c, "a", "1", 10)
	mustPut(c, "b", "2", 100)

	clk.set(15)
	mustPut(c, "c", "3", 100)

	if _, ok := c.Get("a"); ok {
		t.Fatal("expired a should have been evicted")
	}
	if v, ok := c.Get("b"); !ok || v != "2" {
		t.Fatalf("b should survive, got (%q,%v)", v, ok)
	}
	if v, ok := c.Get("c"); !ok || v != "3" {
		t.Fatalf("Get(c) = (%q,%v), want (3,true)", v, ok)
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
}

// 场景二：无过期项时驱逐最久未使用项。
// 容量 2，a、b 均未过期，Get(a) 后写 c：驱逐 b。
func TestEvictLRUWhenNothingExpired(t *testing.T) {
	clk := newClock(0)
	c := mustNew(2, clk)
	mustPut(c, "a", "1", 100)
	mustPut(c, "b", "2", 100)

	if _, ok := c.Get("a"); !ok {
		t.Fatal("Get(a) should hit")
	}
	mustPut(c, "c", "3", 100)

	if _, ok := c.Get("b"); ok {
		t.Fatal("b is LRU and should have been evicted")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a was recently used and should survive")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("c should be present")
	}
}

// 场景三：多个过期项时驱逐「写入时刻最早」者，与访问顺序无关。
func TestEvictEarliestWrittenAmongExpired(t *testing.T) {
	clk := newClock(0)
	c := mustNew(2, clk)
	mustPut(c, "old", "1", 10) // 写入于 t=0
	clk.set(5)
	mustPut(c, "new", "2", 10) // 写入于 t=5

	clk.set(20) // 两者都已过期
	// 访问 old 使其成为“最近使用”，不应影响过期驱逐的选择。
	if _, ok := c.Get("old"); ok {
		t.Fatal("old is expired, Get should miss")
	}
	// old 已被上面的 Get 清理，重新布置：用未访问过的两个过期项。
	c2 := mustNew(2, clk)
	clk.set(0)
	mustPut(c2, "old", "1", 10) // 写入于 t=0
	clk.set(5)
	mustPut(c2, "new", "2", 10) // 写入于 t=5
	clk.set(20)                 // 两者均已过期
	mustPut(c2, "c", "3", 100)  // 应驱逐写入最早的 old

	if c2.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (expired new + c)", c2.Len())
	}
	if _, ok := c2.Get("old"); ok {
		t.Fatal("old (earliest written) should have been evicted")
	}
	if _, ok := c2.Get("new"); ok {
		t.Fatal("new is expired but should still occupy a slot until accessed")
	}
	if c2.Len() != 1 {
		t.Fatalf("Len after expired Get = %d, want 1 (only c)", c2.Len())
	}
	if v, ok := c2.Get("c"); !ok || v != "3" {
		t.Fatalf("Get(c) = (%q,%v), want (3,true)", v, ok)
	}
}

// 过期驱逐只看写入时刻，即使较新的过期项最近被访问过也一样。
func TestEvictExpiredIgnoresRecency(t *testing.T) {
	clk := newClock(0)
	c := mustNew(2, clk)
	mustPut(c, "old", "1", 10) // 写入于 t=0
	clk.set(5)
	mustPut(c, "new", "2", 10) // 写入于 t=5

	// 在 new 尚未过期时访问它，使其成为最近使用。
	clk.set(7)
	if _, ok := c.Get("new"); !ok {
		t.Fatal("Get(new) should hit at t=7")
	}

	clk.set(20) // 两者均已过期
	mustPut(c, "c", "3", 100)

	if _, ok := c.Get("old"); ok {
		t.Fatal("old (earliest written) should have been evicted despite being LRU")
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
}

// 过期项占容量：未清理前 Put 新键会触发驱逐而不是超额。
func TestExpiredEntriesOccupyCapacity(t *testing.T) {
	clk := newClock(0)
	c := mustNew(2, clk)
	mustPut(c, "a", "1", 10)
	mustPut(c, "b", "2", 10)
	clk.set(20)

	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (expired entries still count)", c.Len())
	}
	mustPut(c, "c", "3", 100)
	if c.Len() != 2 {
		t.Fatalf("Len after Put = %d, want 2", c.Len())
	}
}
