// demo 逐条演示 ttlcache 的核心语义，打印 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/internal/ttlcache"
)

var now int64

func clock() int64 { return now }

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
	now = 0
	c, err := ttlcache.New(2, clock)
	check("New(2) 无错误", err == nil)

	_, err = ttlcache.New(0, clock)
	check("New(0) 返回 ErrInvalidCapacity", errors.Is(err, ttlcache.ErrInvalidCapacity))

	err = c.Put("a", "1", 10)
	check("Put(a, ttl=10) 无错误", err == nil)
	check("Put(ttl=0) 返回 ErrInvalidTTL", errors.Is(c.Put("x", "1", 0), ttlcache.ErrInvalidTTL))

	if err := c.Put("b", "2", 100); err != nil {
		check("Put(b, ttl=100) 无错误", false)
	}
	check("Len 计入未清理条目", c.Len() == 2)

	now = 9
	v, ok := c.Get("a")
	check("t=9 时 a 仍有效", ok && v == "1")

	now = 15 // a(0+10) 已过期，b(0+100) 未过期
	if err := c.Put("c", "3", 100); err != nil {
		check("Put(c) 无错误", false)
	}
	_, okA := c.Get("a")
	_, okB := c.Get("b")
	check("满容量时优先驱逐过期项 a，保留 b", !okA && okB)

	now = 0
	c2, _ := ttlcache.New(2, clock)
	_ = c2.Put("k", "v1", 10)
	now = 5
	_ = c2.Put("k", "v2", 10)
	now = 9
	v, ok = c2.Get("k")
	check("同键 Put 后 t=9 拿到新值 v2", ok && v == "v2")
	now = 10
	_, ok = c2.Get("k")
	check("TTL 未刷新，t=10 已过期", !ok)
	check("Get 命中过期项后立即清理", c2.Len() == 0)

	now = 0
	c3, _ := ttlcache.New(2, clock)
	_ = c3.Put("z", "1", 5)
	now = 5
	check("Delete 过期未清理项返回 true", c3.Delete("z"))
	check("Delete 不存在键返回 false", !c3.Delete("z"))

	if failed {
		os.Exit(1)
	}
}
