package ttlcache

import (
	"errors"
	"testing"
)

// clock 是可手动推进的逻辑时钟。
type clock struct{ now int64 }

func (c *clock) Now() int64      { return c.now }
func (c *clock) Advance(d int64) { c.now += d }

func newCache(t *testing.T, capacity int) (*Cache, *clock) {
	t.Helper()
	clk := &clock{}
	c, err := New(capacity, clk.Now)
	if err != nil {
		t.Fatalf("New(%d) 返回意外错误: %v", capacity, err)
	}
	return c, clk
}

func mustPut(t *testing.T, c *Cache, key, val string, ttl int64) {
	t.Helper()
	if err := c.Put(key, val, ttl); err != nil {
		t.Fatalf("Put(%q) 返回意外错误: %v", key, err)
	}
}

func TestNewInvalidCapacity(t *testing.T) {
	for _, capacity := range []int{0, -1, -100} {
		if _, err := New(capacity, func() int64 { return 0 }); !errors.Is(err, ErrInvalidCapacity) {
			t.Errorf("New(%d) 错误 = %v, 期望 ErrInvalidCapacity", capacity, err)
		}
	}
}

func TestPutInvalidTTL(t *testing.T) {
	c, _ := newCache(t, 2)
	for _, ttl := range []int64{0, -1, -100} {
		if err := c.Put("k", "v", ttl); !errors.Is(err, ErrInvalidTTL) {
			t.Errorf("Put ttl=%d 错误 = %v, 期望 ErrInvalidTTL", ttl, err)
		}
	}
	if c.Len() != 0 {
		t.Errorf("非法 Put 后 Len() = %d, 期望 0", c.Len())
	}
}

func TestGetMiss(t *testing.T) {
	c, _ := newCache(t, 2)
	if _, ok := c.Get("nope"); ok {
		t.Error("Get 不存在的键应返回 miss")
	}
}

func TestPutGetBasic(t *testing.T) {
	c, _ := newCache(t, 2)
	mustPut(t, c, "a", "1", 10)
	if val, ok := c.Get("a"); !ok || val != "1" {
		t.Errorf("Get(a) = %q,%v, 期望 \"1\",true", val, ok)
	}
	if c.Len() != 1 {
		t.Errorf("Len() = %d, 期望 1", c.Len())
	}
}

func TestExpiryBoundary(t *testing.T) {
	c, clk := newCache(t, 2)
	mustPut(t, c, "a", "1", 10) // t0=0, 过期点=10

	clk.Advance(9) // 时刻 9：仍有效
	if _, ok := c.Get("a"); !ok {
		t.Error("时刻 9 应仍命中")
	}

	clk.Advance(1) // 时刻 10：到点即过期
	if _, ok := c.Get("a"); ok {
		t.Error("时刻 10 应已过期")
	}
	if c.Len() != 0 {
		t.Errorf("过期 Get 后 Len() = %d, 期望 0", c.Len())
	}
}

func TestExpiredItemCountsInLen(t *testing.T) {
	c, clk := newCache(t, 3)
	mustPut(t, c, "a", "1", 10)
	clk.Advance(10) // a 已过期但未被访问
	if c.Len() != 1 {
		t.Errorf("Len() = %d, 期望 1（过期项仍占容量）", c.Len())
	}
}

func TestPutExistingKeyKeepsTTL(t *testing.T) {
	c, clk := newCache(t, 2)
	mustPut(t, c, "a", "old", 10) // t0=0, 过期点=10

	clk.Advance(5) // 时刻 5：更新值，不刷新 TTL
	mustPut(t, c, "a", "new", 100)

	clk.Advance(4) // 时刻 9：拿到新值
	if val, ok := c.Get("a"); !ok || val != "new" {
		t.Errorf("时刻 9 Get(a) = %q,%v, 期望 \"new\",true", val, ok)
	}

	clk.Advance(1) // 时刻 10：按最初写入时刻已过期
	if _, ok := c.Get("a"); ok {
		t.Error("时刻 10 应已过期（TTL 未被刷新）")
	}
}

func TestDelete(t *testing.T) {
	c, clk := newCache(t, 2)
	mustPut(t, c, "a", "1", 10)

	if c.Delete("nope") {
		t.Error("Delete 不存在的键应返回 false")
	}

	clk.Advance(10) // a 已过期但未清理
	if !c.Delete("a") {
		t.Error("Delete 已过期未清理的项应返回 true")
	}
	if c.Len() != 0 {
		t.Errorf("Delete 后 Len() = %d, 期望 0", c.Len())
	}
	if c.Delete("a") {
		t.Error("重复 Delete 应返回 false")
	}
}
