// demo 逐条演示 ttlcache 的核心语义并打印 OK/FAIL 判定。
package main

import (
	"fmt"
	"os"

	"ontology/internal/ttlcache"
)

var failed bool

func check(name string, cond bool) {
	status := "OK"
	if !cond {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("[%s] %s\n", status, name)
}

func main() {
	var now int64
	clock := func() int64 { return now }

	c, err := ttlcache.New(2, clock)
	check("New(2) succeeds", err == nil && c != nil)
	_, err = ttlcache.New(0, clock)
	check("New(0) returns ErrInvalidCapacity", err == ttlcache.ErrInvalidCapacity)
	check("Put ttl=0 returns ErrInvalidTTL", c.Put("x", "1", 0) == ttlcache.ErrInvalidTTL)

	// 场景一：满容量时优先驱逐已过期的 a。
	now = 0
	_ = c.Put("a", "1", 10)
	_ = c.Put("b", "2", 100)
	now = 15
	_ = c.Put("c", "3", 100)
	_, okA := c.Get("a")
	vB, okB := c.Get("b")
	check("expired a evicted, b kept", !okA && okB && vB == "2")

	// 场景二：无过期项时驱逐最久未使用的 b。
	now = 0
	c, _ = ttlcache.New(2, clock)
	_ = c.Put("a", "1", 100)
	_ = c.Put("b", "2", 100)
	_, _ = c.Get("a")
	_ = c.Put("c", "3", 100)
	_, okB = c.Get("b")
	check("LRU b evicted after Get(a)", !okB)

	// 场景三：多个过期项驱逐写入时刻最早者。
	now = 0
	c, _ = ttlcache.New(2, clock)
	_ = c.Put("old", "1", 10)
	now = 5
	_ = c.Put("new", "2", 10)
	now = 20
	_ = c.Put("c", "3", 100)
	_, okOld := c.Get("old")
	check("earliest-written expired evicted", !okOld && c.Len() == 2)

	// 场景四：重复 Put 不刷新 TTL。
	now = 0
	c, _ = ttlcache.New(2, clock)
	_ = c.Put("a", "old", 10)
	now = 5
	_ = c.Put("a", "new", 10)
	now = 9
	v, ok := c.Get("a")
	check("updated value visible at t=9", ok && v == "new")
	now = 10
	_, ok = c.Get("a")
	check("TTL not refreshed, miss at t=10", !ok)

	// 过期项占容量；Get 命中过期项即清理。
	now = 0
	c, _ = ttlcache.New(2, clock)
	_ = c.Put("a", "1", 10)
	now = 15
	check("expired entry counts in Len", c.Len() == 1)
	_, _ = c.Get("a")
	check("expired Get removes entry", c.Len() == 0)

	if failed {
		os.Exit(1)
	}
}
