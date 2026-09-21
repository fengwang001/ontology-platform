package ttlcache

import "testing"

func TestPutGetBasic(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "1", 100)
	mustGet(t, c, "a", "1")
	mustMiss(t, c, "missing")

	if got := c.Len(); got != 1 {
		t.Fatalf("Len() = %d, 期望 1", got)
	}
}

func TestPutExistingKeyUpdatesValueOnly(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "old", 100)
	mustPut(t, c, "a", "new", 100)

	mustGet(t, c, "a", "new")
	if got := c.Len(); got != 1 {
		t.Fatalf("Len() = %d, 期望 1（同键 Put 不新增条目）", got)
	}
}

func TestDelete(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "1", 100)
	if !c.Delete("a") {
		t.Fatal("Delete(存在的键) 应返回 true")
	}
	if c.Delete("a") {
		t.Fatal("Delete(不存在的键) 应返回 false")
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("Len() = %d, 期望 0", got)
	}
}

func TestDeleteExpiredButUncleanedCounts(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "1", 10)
	clk.set(10) // a 已过期但未被清理

	if got := c.Len(); got != 1 {
		t.Fatalf("Len() = %d, 期望 1（过期项仍占容量）", got)
	}
	if !c.Delete("a") {
		t.Fatal("Delete(已过期未清理的键) 应返回 true")
	}
	if got := c.Len(); got != 0 {
		t.Fatalf("Len() = %d, 期望 0", got)
	}
}

func TestGetPromotesToMostRecentlyUsed(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)

	mustPut(t, c, "a", "1", 100)
	mustPut(t, c, "b", "2", 100)
	mustGet(t, c, "a", "1") // a 变为最近使用，b 成为最久未使用

	mustPut(t, c, "c", "3", 100) // 应驱逐 b
	mustMiss(t, c, "b")
	mustGet(t, c, "a", "1")
	mustGet(t, c, "c", "3")
}
