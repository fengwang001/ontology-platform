package ttlcache

import "testing"

// 到点即过期：t0+d 已过期，t0+d-1 仍有效。
func TestExpiryBoundary(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "1", 10) // t0=0, 过期时刻=10

	clk.set(9)
	mustGet(t, c, "a", "1")

	clk.set(10)
	mustMiss(t, c, "a")
	if got := c.Len(); got != 0 {
		t.Fatalf("Len() = %d, 期望 0（Get 命中过期项应立即删除）", got)
	}
}

// 过期项在被访问前一直占用容量，Len 计入。
func TestExpiredItemStillOccupiesCapacity(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "1", 10)
	clk.set(50)

	if got := c.Len(); got != 1 {
		t.Fatalf("Len() = %d, 期望 1（过期项未清理前仍计入）", got)
	}
}

// Get 命中过期项：返回 miss 且立即删除，Len 减一。
func TestGetOnExpiredRemovesImmediately(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "1", 10)
	mustPut(t, c, "b", "2", 1000)
	clk.set(10)

	mustMiss(t, c, "a")
	if got := c.Len(); got != 1 {
		t.Fatalf("Len() = %d, 期望 1", got)
	}
	mustGet(t, c, "b", "2")
}

// 重复 Put 同键不刷新 TTL：过期时刻仍以首次写入为准。
func TestRePutDoesNotRefreshTTL(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "v1", 10) // t0=0, 过期时刻=10
	clk.set(5)
	mustPut(t, c, "a", "v2", 10) // 只更新值，不刷新 TTL

	clk.set(9)
	mustGet(t, c, "a", "v2") // 拿到新值

	clk.set(10)
	mustMiss(t, c, "a") // TTL 未刷新，到点过期
}
