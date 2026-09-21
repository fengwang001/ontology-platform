package ttlcache

import "testing"

// 容量满且存在过期项时，驱逐过期项而非 LRU 项。
func TestEvictPrefersExpired(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "1", 10)
	mustPut(t, c, "b", "2", 100)
	clk.set(15) // a 已过期，b 未过期

	mustPut(t, c, "c", "3", 100) // 应驱逐 a，保留 b
	mustMiss(t, c, "a")
	mustGet(t, c, "b", "2")
	mustGet(t, c, "c", "3")
}

// 无过期项时驱逐最久未使用的项。
func TestEvictLRUWhenNoneExpired(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "1", 100)
	mustPut(t, c, "b", "2", 100)
	mustGet(t, c, "a", "1") // b 成为最久未使用

	mustPut(t, c, "c", "3", 100) // 应驱逐 b
	mustMiss(t, c, "b")
	mustGet(t, c, "a", "1")
	mustGet(t, c, "c", "3")
}

// 多个过期项时驱逐写入时刻最早者，与最近访问无关。
func TestEvictEarliestWrittenAmongExpired(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "1", 10) // 写入时刻 0
	clk.set(1)
	mustPut(t, c, "b", "2", 10) // 写入时刻 1
	clk.set(20)                 // a、b 都已过期

	// 触发驱逐：应驱逐写入时刻最早的 a，而非 b。
	mustPut(t, c, "c", "3", 1000)
	if got := c.Len(); got != 2 {
		t.Fatalf("Len() = %d, 期望 2", got)
	}
	mustMiss(t, c, "a") // a 已被驱逐
	mustGet(t, c, "c", "3")
	// b 也已过期但未被驱逐，仍占一个容量位。
	if got := c.Len(); got != 2 {
		t.Fatalf("Len() = %d, 期望 2（b 应仍在缓存中）", got)
	}
}

// 过期项的访问时间不影响「按写入时刻最早」的驱逐选择。
func TestEvictExpiredIgnoresRecency(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 3, clk)

	mustPut(t, c, "old", "1", 10) // 写入时刻 0
	clk.set(1)
	mustPut(t, c, "new", "2", 20) // 写入时刻 1
	clk.set(2)
	mustPut(t, c, "live", "3", 1000)

	clk.set(15)                   // old 已过期，new 未过期(1+20=21)，live 未过期
	mustPut(t, c, "x", "4", 1000) // 应驱逐 old
	mustMiss(t, c, "old")
	mustGet(t, c, "new", "2")
	mustGet(t, c, "live", "3")
	mustGet(t, c, "x", "4")
}
