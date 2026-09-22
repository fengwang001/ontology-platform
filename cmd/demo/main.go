// demo 逐条演示 ttlcache 的核心语义并打印 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"strconv"

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

// lastExamined 读取缓存内部非导出的驱逐考察计数（仅供演示观测）。
func lastExamined(c *ttlcache.Cache) int {
	return int(reflect.ValueOf(c).Elem().FieldByName("lastExamined").Int())
}

// massExpiryExamined 装满 n 项并让它们同时过期，再触发一次驱逐，
// 返回这次驱逐考察的候选数。
func massExpiryExamined(n int) int {
	clk := &clock{}
	c := mustCache(n, clk)
	for i := 0; i < n; i++ {
		_ = c.Put("k"+strconv.Itoa(i), "v", 10)
	}
	clk.Advance(10) // 全部同时过期
	_ = c.Put("trigger", "v", 100)
	return lastExamined(c)
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

	// 场景 5：并列写入时刻的驱逐规则（重构后原样保留）。
	clk5 := &clock{}
	c5 := mustCache(2, clk5)
	_ = c5.Put("a", "1", 10)
	_ = c5.Put("b", "2", 10)
	clk5.Advance(10)
	_ = c5.Put("c", "3", 100)
	check("并列且均未访问: 驱逐最近使用的 b", !c5.Delete("b") && c5.Delete("a"))

	clk6 := &clock{}
	c6 := mustCache(2, clk6)
	_ = c6.Put("a", "1", 10)
	_ = c6.Put("b", "2", 10)
	clk6.Advance(5)
	c6.Get("a") // a 提升为最近使用
	clk6.Advance(5)
	_ = c6.Put("c", "3", 100)
	check("并列但 a 被命中提升: 驱逐 a", !c6.Delete("a") && c6.Delete("b"))

	// 场景 6：大量项同时过期时，驱逐考察的候选数不随规模线性增长。
	e100 := massExpiryExamined(100)
	e1000 := massExpiryExamined(1000)
	check(fmt.Sprintf("N=100 同时过期, 驱逐考察 %d 个候选", e100), e100 > 0 && e100 <= 20)
	check(fmt.Sprintf("N=1000 同时过期, 驱逐考察 %d 个候选(非线性)", e1000), e1000 > 0 && e1000 <= e100*2+4)

	if failed {
		os.Exit(1)
	}
}
