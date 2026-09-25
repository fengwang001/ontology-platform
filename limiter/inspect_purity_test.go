package limiter

import (
	"testing"
	"time"

	"ontology/policy"
)

// 钉住的语义（任务一.7）：Inspect 是只读查询——同一时刻连续两次结果完全
// 相同；两次查询之间推进时钟，结果按速率精确变化；关键地，查询不消耗
// 令牌：查到的余量随后可以被 Allow 全额取走。
// 之前未覆盖：既有 TestInspectIsPureAndStable 验证了「两次相同」和
// 「数值正确」，但从未在 Inspect 之后用 Allow 把查到的余额全额取走来
// 证明查询没有副作用（若 Inspect 暗扣令牌，全额 Allow 会失败）。
func TestInspectDoesNotConsume(t *testing.T) {
	l, c := newLimiter(t, 20, 1, map[string]policy.Quota{
		"a": policy.Must(10, 2),
	})
	if err := l.Allow("a", 4); err != nil { // 租户 6，全局 16
		t.Fatal(err)
	}

	c.Advance(time.Second) // 租户 6+2=8，全局 16+1=17
	tb1, gb1, q1, ok1 := l.Inspect("a")
	tb2, gb2, q2, ok2 := l.Inspect("a")
	if !ok1 || !ok2 {
		t.Fatal("tenant should exist")
	}
	if tb1 != tb2 || gb1 != gb2 || q1 != q2 {
		t.Fatalf("same-instant inspects differ: (%v,%v,%v) vs (%v,%v,%v)",
			tb1, gb1, q1, tb2, gb2, q2)
	}
	if tb1 != 8 || gb1 != 17 {
		t.Fatalf("inspect = (%v,%v), want (8,17)", tb1, gb1)
	}

	c.Advance(500 * time.Millisecond) // 租户 8+1=9，全局 17+0.5=17.5
	tb3, gb3, _, _ := l.Inspect("a")
	if tb3 != 9 || gb3 != 17.5 {
		t.Fatalf("inspect after 0.5s = (%v,%v), want (9,17.5)", tb3, gb3)
	}

	// 把查到的 9 个全额取走：若查询消耗过令牌，这里必然失败。
	if err := l.Allow("a", 9); err != nil {
		t.Fatalf("inspect must not consume: full allowance of inspected balance failed: %v", err)
	}
	tb4, gb4, _, _ := l.Inspect("a")
	if tb4 != 0 || gb4 != 8.5 { // 全局 17.5-9=8.5
		t.Fatalf("after full take: (%v,%v), want (0,8.5)", tb4, gb4)
	}
}

// 钉住的语义（任务一.7 的退化形态）：速率为 0 时，无论时钟怎么推进，
// 连续查询的结果都完全相同——查询本身不会随时间产生任何变化。
// 之前未覆盖：既有纯度测试的桶都带速率，零速率下「推进时钟也不变」
// 这一半从未被断言。
func TestInspectStableWithZeroRates(t *testing.T) {
	l, c := newLimiter(t, 20, 0, map[string]policy.Quota{
		"a": policy.Must(10, 0),
	})
	if err := l.Allow("a", 3); err != nil { // 租户 7，全局 17
		t.Fatal(err)
	}
	c.Advance(time.Hour) // 速率 0：时间流逝不改变任何余量

	first1, first2, _, _ := l.Inspect("a")
	second1, second2, _, _ := l.Inspect("a")
	if first1 != second1 || first2 != second2 {
		t.Fatalf("zero-rate inspects differ: (%v,%v) vs (%v,%v)", first1, first2, second1, second2)
	}
	if first1 != 7 || first2 != 17 {
		t.Fatalf("zero-rate balances must not move with time: (%v,%v), want (7,17)", first1, first2)
	}
}
