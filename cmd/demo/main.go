// demo 逐条演示 ttlcache 的核心语义并打印 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"unsafe"

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
	fmt.Printf("[%s] %s\n", status, name)
}

func mustCache(capacity int, clk *clock) *ttlcache.Cache {
	c, err := ttlcache.New(capacity, clk.Now)
	if err != nil {
		fmt.Println("[FAIL] New 返回错误:", err)
		os.Exit(1)
	}
	return c
}

// lastExamined 读取缓存内部非导出的驱逐考察计数。
// 计数器刻意不在公开接口里，这里只能用反射读取。
func lastExamined(c *ttlcache.Cache) int64 {
	f := reflect.ValueOf(c).Elem().FieldByName("lastEvictExamined")
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Int()
}

func main() {
	_, err := ttlcache.New(0, func() int64 { return 0 })
	check("capacity<=0 返回 ErrInvalidCapacity", errors.Is(err, ttlcache.ErrInvalidCapacity))

	clk := &clock{}
	c := mustCache(2, clk)
	check("ttl<=0 返回 ErrInvalidTTL", errors.Is(c.Put("x", "1", 0), ttlcache.ErrInvalidTTL))

	_ = c.Put("a", "1", 10)
	clk.Advance(9)
	_, ok9 := c.Get("a")
	clk.Advance(1)
	_, ok10 := c.Get("a")
	check("到点即过期: t0+9 命中, t0+10 miss 且删除", ok9 && !ok10 && c.Len() == 0)

	// 场景 1：容量满时优先驱逐已过期项。
	clk1 := &clock{}
	c1 := mustCache(2, clk1)
	_ = c1.Put("a", "1", 10)
	_ = c1.Put("b", "2", 100)
	clk1.Advance(15)
	_ = c1.Put("c", "3", 100)
	_, okA := c1.Get("a")
	_, okB := c1.Get("b")
	check("满时驱逐过期项 a, 保留 b", !okA && okB)

	// 场景 2：无过期项时驱逐最久未使用者。
	c2 := mustCache(2, &clock{})
	_ = c2.Put("a", "1", 100)
	_ = c2.Put("b", "2", 100)
	c2.Get("a")
	_ = c2.Put("c", "3", 100)
	_, okB2 := c2.Get("b")
	check("无过期项驱逐最久未使用的 b", !okB2)

	// 场景 3：多个过期项驱逐写入时刻最早者。
	clk3 := &clock{}
	c3 := mustCache(2, clk3)
	_ = c3.Put("a", "1", 10)
	clk3.Advance(1)
	_ = c3.Put("b", "2", 10)
	clk3.Advance(4)
	c3.Get("a") // a 提升为最近使用
	clk3.Advance(6)
	_ = c3.Put("c", "3", 100)
	check("驱逐写入最早的 a, b 过期仍占位", c3.Delete("b") && !c3.Delete("a"))

	// 场景 4：重复 Put 不刷新 TTL。
	clk4 := &clock{}
	c4 := mustCache(2, clk4)
	_ = c4.Put("a", "old", 10)
	clk4.Advance(5)
	_ = c4.Put("a", "new", 100)
	clk4.Advance(4)
	v9, ok94 := c4.Get("a")
	clk4.Advance(1)
	_, ok104 := c4.Get("a")
	check("重复 Put 不刷新 TTL: t9 得新值, t10 过期", ok94 && v9 == "new" && !ok104)

	// 场景 5：写入时刻并列且均未访问，驱逐并列中最近使用的 b。
	clk5 := &clock{}
	c5 := mustCache(2, clk5)
	_ = c5.Put("a", "1", 10)
	_ = c5.Put("b", "2", 10) // 与 a 同一时刻写入
	clk5.Advance(10)
	_ = c5.Put("c", "3", 100)
	check("并列均未访问: 驱逐最近使用的 b", !c5.Delete("b") && c5.Delete("a"))

	// 场景 6：并列但 a 被 Get 提升为最近使用，驱逐的变成 a。
	clk6 := &clock{}
	c6 := mustCache(2, clk6)
	_ = c6.Put("a", "1", 10)
	_ = c6.Put("b", "2", 10)
	clk6.Advance(5)
	c6.Get("a")
	clk6.Advance(5)
	_ = c6.Put("c", "3", 100)
	check("并列但 a 被提升: 驱逐 a", !c6.Delete("a") && c6.Delete("b"))

	// 场景 7：N 项同时过期时，单次驱逐考察的候选数不随 N 线性增长。
	examined := func(n int) int64 {
		clkN := &clock{}
		cN := mustCache(n, clkN)
		for i := 0; i < n; i++ {
			_ = cN.Put(fmt.Sprintf("k%d", i), "v", 10)
		}
		clkN.Advance(10)
		_ = cN.Put("trigger", "v", 100)
		return lastExamined(cN)
	}
	e100, e1000 := examined(100), examined(1000)
	fmt.Printf("       N=100 考察 %d 项, N=1000 考察 %d 项\n", e100, e1000)
	check("驱逐考察数不随 N 线性增长", e1000 <= e100*2+16 && e1000 <= 64)

	if failed {
		os.Exit(1)
	}
}
