package ttlcache

import "testing"

// 写入时刻并列的驱逐规则探查测试：钉住现状实现（线性扫描 evict）在
// 多个已过期项 writeAt 相同时的实际取舍，重构后必须逐位保持。
//
// 现状 evict 从 LRU 队首（最近使用）向队尾扫描，仅以严格小于更新
// "写入时刻最早者"，因此并列时胜出的是扫描中先遇到的那个——
// 即并列项中最近使用（离队首最近）者。

// 情形一：并列的几项都没有被额外访问过。
// 队首→队尾为 [b, a]，两者写入时刻相同且均已过期，应驱逐 b。
func TestEvictTieNoExtraAccess(t *testing.T) {
	c, clk := newCache(t, 2)
	mustPut(t, c, "a", "1", 10) // t0=0, 过期点=10
	mustPut(t, c, "b", "2", 10) // t0=0, 过期点=10（与 a 并列）

	clk.Advance(10) // 时刻 10：a、b 同时过期
	mustPut(t, c, "c", "3", 100)

	if c.Delete("b") {
		t.Error("b 应被驱逐（并列项中最近使用者）")
	}
	if !c.Delete("a") {
		t.Error("a 应保留（并列项中较久未使用者）")
	}
	if !c.Delete("c") {
		t.Error("c 应存在")
	}
}

// 情形二：并列项中某一项因 Get 命中被提升为最近使用。
// 队首→队尾变为 [a, b]，应驱逐 a——与情形一被驱逐的不是同一个。
func TestEvictTieAfterGetPromote(t *testing.T) {
	c, clk := newCache(t, 2)
	mustPut(t, c, "a", "1", 10) // t0=0, 过期点=10
	mustPut(t, c, "b", "2", 10) // t0=0, 过期点=10（与 a 并列）

	clk.Advance(5) // 时刻 5：a 仍有效，Get 把它提升为最近使用
	if _, ok := c.Get("a"); !ok {
		t.Fatal("时刻 5 Get(a) 应命中")
	}

	clk.Advance(5) // 时刻 10：a、b 同时过期，a 是最近使用者
	mustPut(t, c, "c", "3", 100)

	if c.Delete("a") {
		t.Error("a 应被驱逐（并列项中最近使用者）")
	}
	if !c.Delete("b") {
		t.Error("b 应保留")
	}
}

// 情形三：三项并列，被提升过的一项成为并列中的最近使用者而被驱逐。
func TestEvictTieThreeWay(t *testing.T) {
	c, clk := newCache(t, 3)
	mustPut(t, c, "a", "1", 10)
	mustPut(t, c, "b", "2", 10)
	mustPut(t, c, "c", "3", 10) // 三者 t0 均为 0

	clk.Advance(5)
	if _, ok := c.Get("b"); !ok { // b 提升为最近使用
		t.Fatal("时刻 5 Get(b) 应命中")
	}

	clk.Advance(5) // 时刻 10：三者同时过期，队首→队尾为 [b, c, a]
	mustPut(t, c, "d", "4", 100)

	if c.Delete("b") {
		t.Error("b 应被驱逐（并列项中最近使用者）")
	}
	if !c.Delete("a") || !c.Delete("c") {
		t.Error("a、c 应保留")
	}
}
