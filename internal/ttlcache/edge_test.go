package ttlcache

import (
	"fmt"
	"sync"
	"testing"
)

// 容量为 1 时，每次 Put 都驱逐上一项。
func TestCapacityOne(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 1, clk)
	mustPut(t, c, "a", "1", 100)
	mustPut(t, c, "b", "2", 100)

	if _, ok := c.Get("a"); ok {
		t.Error("Get(a) = hit, want evicted")
	}
	if val, ok := c.Get("b"); !ok || val != "2" {
		t.Errorf("Get(b) = %q, %v; want %q, true", val, ok, "2")
	}
	if c.Len() != 1 {
		t.Errorf("Len() = %d, want 1", c.Len())
	}
}

// 容量满且所有项都已过期时，新项只驱逐一个过期项，其余过期项保留占位。
func TestEvictOnlyOneExpiredPerPut(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 3, clk)
	mustPut(t, c, "a", "1", 10)
	mustPut(t, c, "b", "2", 10)
	mustPut(t, c, "c", "3", 10)

	clk.Advance(10) // 全部过期
	mustPut(t, c, "d", "4", 100)

	if c.Len() != 3 {
		t.Errorf("Len() = %d, want 3 (only one expired entry evicted)", c.Len())
	}
	if _, ok := c.Get("d"); !ok {
		t.Error("Get(d) = miss, want hit")
	}
}

// 空缓存上的操作安全且行为正确。
func TestEmptyCache(t *testing.T) {
	c := newCache(t, 2, &clock{})
	if _, ok := c.Get("x"); ok {
		t.Error("Get on empty cache = hit, want miss")
	}
	if c.Delete("x") {
		t.Error("Delete on empty cache = true, want false")
	}
	if c.Len() != 0 {
		t.Errorf("Len() = %d, want 0", c.Len())
	}
}

// 过期项被 Get 清理后，腾出的容量可直接写入而不再驱逐。
func TestExpiredCleanupFreesCapacity(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 2, clk)
	mustPut(t, c, "a", "1", 10)
	mustPut(t, c, "b", "2", 1000)

	clk.Advance(10)
	if _, ok := c.Get("a"); ok { // 清理 a
		t.Fatal("Get(a) expired = hit, want miss")
	}
	mustPut(t, c, "c", "3", 1000) // 不应驱逐 b

	if _, ok := c.Get("b"); !ok {
		t.Error("Get(b) = miss, want hit")
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("Get(c) = miss, want hit")
	}
}

// 并发读写：在 -race 下验证无数据竞争，且 Len 永不超过容量。
func TestConcurrentAccess(t *testing.T) {
	clk := &clock{}
	c := newCache(t, 8, clk)

	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("k%d", (id*200+i)%32)
				_ = c.Put(key, "v", 1000)
				_, _ = c.Get(key)
				c.Delete(key)
				if c.Len() > 8 {
					t.Errorf("Len() = %d exceeds capacity 8", c.Len())
				}
			}
		}(worker)
	}
	wg.Wait()
}
