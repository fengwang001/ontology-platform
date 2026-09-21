package ttlcache

import "testing"

// clock 是测试用的手动时钟，时间只能由测试显式推进。
type clock struct {
	t int64
}

func (c *clock) now() int64 { return c.t }

func (c *clock) advance(d int64) { c.t += d }

func (c *clock) set(t int64) { c.t = t }

func newCache(t *testing.T, capacity int, clk *clock) *Cache {
	t.Helper()
	c, err := New(capacity, clk.now)
	if err != nil {
		t.Fatalf("New(%d) 返回意外错误: %v", capacity, err)
	}
	return c
}

func mustPut(t *testing.T, c *Cache, key, val string, ttl int64) {
	t.Helper()
	if err := c.Put(key, val, ttl); err != nil {
		t.Fatalf("Put(%q) 返回意外错误: %v", key, err)
	}
}

func mustGet(t *testing.T, c *Cache, key, want string) {
	t.Helper()
	got, ok := c.Get(key)
	if !ok || got != want {
		t.Fatalf("Get(%q) = (%q, %v), 期望 (%q, true)", key, got, ok, want)
	}
}

func mustMiss(t *testing.T, c *Cache, key string) {
	t.Helper()
	if got, ok := c.Get(key); ok {
		t.Fatalf("Get(%q) = (%q, true), 期望 miss", key, got)
	}
}
