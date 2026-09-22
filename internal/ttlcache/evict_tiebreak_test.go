package ttlcache

import "testing"

// 本文件在重构前刻画并钉住一个未写进任何注释的现存行为：
// 多个已过期项的写入时刻完全相同时，evict 驱逐其中“最近使用”的项
// （即 LRU 链中最靠前者），而不是最久未使用者。
// 重构后这些测试必须一行不改地继续通过。

// 情形一：并列的几项都没有被额外访问过。
// a、b 同一时刻写入，LRU 链从前到后为 b、a；并列时驱逐最靠前者 b。
func TestEvictTieNoExtraAccess(t *testing.T) {
	c, clk := newCache(t, 2)
	mustPut(t, c, "a", "1", 10)
	mustPut(t, c, "b", "2", 10) // 与 a 同一写入时刻

	clk.Advance(10) // a、b 同时过期
	mustPut(t, c, "c", "3", 100)

	if c.Len() != 2 {
		t.Fatalf("Len() = %d, 期望 2", c.Len())
	}
	if c.Delete("b") {
		t.Error("并列且均未访问时，应驱逐最近使用的 b")
	}
	if !c.Delete("a") {
		t.Error("a 应保留")
	}
	if !c.Delete("c") {
		t.Error("c 应存在")
	}
}

// 情形二：并列项中 a 因 Get 命中被提升为最近使用。
// 被驱逐的随之变成 a，与情形一不是同一项。
func TestEvictTieAfterGetPromotes(t *testing.T) {
	c, clk := newCache(t, 2)
	mustPut(t, c, "a", "1", 10)
	mustPut(t, c, "b", "2", 10)

	clk.Advance(5) // 时刻 5：均仍有效
	if _, ok := c.Get("a"); !ok {
		t.Fatal("Get(a) 应命中")
	}

	clk.Advance(5) // 时刻 10：a、b 同时过期，a 是最近使用者
	mustPut(t, c, "c", "3", 100)

	if c.Delete("a") {
		t.Error("a 被提升为最近使用后，并列时应驱逐 a")
	}
	if !c.Delete("b") {
		t.Error("b 应保留")
	}
	if !c.Delete("c") {
		t.Error("c 应存在")
	}
}

// 情形三：三项写入时刻并列，另有一项写入更晚但过期点相同。
// 并列组中驱逐最近使用的 c；写入更晚的 d 不参与并列，本轮保留。
func TestEvictTieThreeWayWithLaterWrite(t *testing.T) {
	c, clk := newCache(t, 4)
	mustPut(t, c, "a", "1", 10) // t0=0
	mustPut(t, c, "b", "2", 10) // t0=0
	mustPut(t, c, "c", "3", 10) // t0=0
	clk.Advance(1)
	mustPut(t, c, "d", "4", 9) // t0=1, 过期点同为 10

	clk.Advance(9) // 时刻 10：四项全部过期
	mustPut(t, c, "e", "5", 100)

	if c.Delete("c") {
		t.Error("a、b、c 并列，应驱逐其中最近使用的 c")
	}
	for _, k := range []string{"a", "b", "d", "e"} {
		if !c.Delete(k) {
			t.Errorf("%s 应保留", k)
		}
	}
}
