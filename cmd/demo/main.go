// demo 逐条演示 ttlcache 的关键语义并打印 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/internal/ttlcache"
)

type clock struct{ now int64 }

func (c *clock) Now() int64      { return c.now }
func (c *clock) Advance(d int64) { c.now += d }

var failed bool

func check(name string, cond bool) {
	status := "OK"
	if !cond {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%-4s %s\n", status, name)
}

func mustCache(capacity int, c *clock) *ttlcache.Cache {
	cache, err := ttlcache.New(capacity, c.Now)
	if err != nil {
		fmt.Println("FAIL New:", err)
		os.Exit(1)
	}
	return cache
}

func main() {
	// 1. 到点即过期：t0+d 已过期，t0+d-1 有效。
	clk := &clock{}
	c := mustCache(2, clk)
	_ = c.Put("a", "1", 10)
	clk.Advance(9)
	_, hit9 := c.Get("a")
	clk.Advance(1)
	_, hit10 := c.Get("a")
	check("expire-at t0+d", hit9 && !hit10)

	// 2. 过期项占容量，Len 计入；Get 命中过期项即删。
	clk = &clock{}
	c = mustCache(2, clk)
	_ = c.Put("a", "1", 10)
	clk.Advance(10)
	lenBefore := c.Len()
	_, ok := c.Get("a")
	check("expired counts in Len; Get purges", lenBefore == 1 && !ok && c.Len() == 0)

	// 3. 容量满优先驱逐过期项。
	clk = &clock{}
	c = mustCache(2, clk)
	_ = c.Put("a", "1", 10)
	_ = c.Put("b", "2", 100)
	clk.Advance(15)
	_ = c.Put("c", "3", 100)
	_, okA := c.Get("a")
	_, okB := c.Get("b")
	check("evict expired first", !okA && okB)

	// 4. 无过期项时驱逐最久未使用项。
	clk = &clock{}
	c = mustCache(2, clk)
	_ = c.Put("a", "1", 1000)
	_ = c.Put("b", "2", 1000)
	_, _ = c.Get("a")
	_ = c.Put("c", "3", 1000)
	_, okB = c.Get("b")
	check("evict LRU when none expired", !okB)

	// 5. 多个过期项驱逐写入最早者。
	clk = &clock{}
	c = mustCache(2, clk)
	_ = c.Put("a", "1", 10)
	clk.Advance(5)
	_ = c.Put("b", "2", 10)
	clk.Advance(5)
	_ = c.Put("c", "3", 100)
	_, okA = c.Get("a")
	_, okB = c.Get("b")
	check("evict earliest-written expired", !okA && okB)

	// 6. 重复 Put 不刷新 TTL。
	clk = &clock{}
	c = mustCache(2, clk)
	_ = c.Put("a", "old", 10)
	clk.Advance(5)
	_ = c.Put("a", "new", 10)
	clk.Advance(4)
	v, okA := c.Get("a")
	clk.Advance(1)
	_, okA10 := c.Get("a")
	check("re-put keeps original TTL", okA && v == "new" && !okA10)

	// 7. 参数校验与 Delete 语义。
	_, errCap := ttlcache.New(0, clk.Now)
	errTTL := c.Put("x", "y", 0)
	del := c.Delete("ghost")
	check("sentinel errors; delete miss=false",
		errors.Is(errCap, ttlcache.ErrInvalidCapacity) &&
			errors.Is(errTTL, ttlcache.ErrInvalidTTL) && !del)

	if failed {
		os.Exit(1)
	}
}
