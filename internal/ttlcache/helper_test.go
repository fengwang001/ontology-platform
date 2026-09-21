package ttlcache

import "sync/atomic"

// clock 是可手动推进的测试时钟。
type clock struct {
	ms atomic.Int64
}

func newClock(start int64) *clock {
	c := &clock{}
	c.ms.Store(start)
	return c
}

func (c *clock) now() int64 { return c.ms.Load() }

func (c *clock) advance(d int64) { c.ms.Add(d) }

func (c *clock) set(t int64) { c.ms.Store(t) }

// mustNew 创建缓存，失败即 panic。
func mustNew(capacity int, clk *clock) *Cache {
	c, err := New(capacity, clk.now)
	if err != nil {
		panic(err)
	}
	return c
}

// mustPut 写入缓存，失败即 panic。
func mustPut(c *Cache, key, val string, ttl int64) {
	if err := c.Put(key, val, ttl); err != nil {
		panic(err)
	}
}
