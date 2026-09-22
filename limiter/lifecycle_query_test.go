package limiter

import (
	"testing"
	"time"

	"ontology/policy"
)

// 钉住的语义：注销与重建——注销时桶里即使有"因时间流逝应得但尚未结算"
// 的补充，重建后也只能是一个全新的满桶，那部分补充不得带过来；
// 注销与重建全程不影响全局桶余量。
// 之前未被覆盖的原因：TestUnregisterAndRecreate 使用速率 0 且不推进时钟，
// 桶里不存在待结算补充，无法暴露"旧桶状态泄漏到新桶"的问题。
func TestUnregisterDropsPendingRefill(t *testing.T) {
	l, c := newLimiter(t, 100, 0, map[string]policy.Quota{
		"a": policy.Must(10, 1),
	})
	if err := l.Allow("a", 10); err != nil { // 抽干，global: 90
		t.Fatal(err)
	}
	c.Advance(5 * time.Second) // 待补充 5，未查询
	if err := l.Unregister("a"); err != nil {
		t.Fatal(err)
	}
	if err := l.Register("a", policy.Must(10, 1)); err != nil {
		t.Fatal(err)
	}
	tb, gb := balances(t, l, "a")
	if tb != 10 {
		t.Fatalf("re-registered tenant must be a fresh full bucket, got %v, want 10", tb)
	}
	if gb != 90 {
		t.Fatalf("unregister/register must not touch global, got %v, want 90", gb)
	}
	c.Advance(3 * time.Second) // 新桶从注册时刻起算，满桶不再增长
	if tb, _ := balances(t, l, "a"); tb != 10 {
		t.Fatalf("full fresh bucket must stay capped at 10, got %v", tb)
	}
	if _, gb := balances(t, l, "a"); gb != 90 {
		t.Fatalf("global must remain 90, got %v", gb)
	}
}

// 钉住的语义：查询纯度——同一时刻连续两次 Inspect 结果完全相同；
// 两次查询之间推进时钟，结果严格按速率变化；查询本身不消耗令牌
// （查到的余量随后可以全额取走）。
// 之前未被覆盖的原因：TestInspectIsPureAndStable 验证了同刻重复查询一致，
// 但没有验证"查询之间推进时钟按速率变化"，也没有用 Allow 证明查询不消耗。
func TestInspectPureAcrossClockAdvances(t *testing.T) {
	l, c := newLimiter(t, 100, 1, map[string]policy.Quota{
		"a": policy.Must(10, 2),
	})
	if err := l.Allow("a", 10); err != nil { // a: 0, global: 90
		t.Fatal(err)
	}
	c.Advance(2 * time.Second) // a: 4, global: 92
	tb1, gb1, q1, ok1 := l.Inspect("a")
	tb2, gb2, q2, ok2 := l.Inspect("a")
	if !ok1 || !ok2 {
		t.Fatal("tenant should exist")
	}
	if tb1 != tb2 || gb1 != gb2 || q1 != q2 {
		t.Fatalf("same-instant inspects differ: (%v,%v,%v) vs (%v,%v,%v)",
			tb1, gb1, q1, tb2, gb2, q2)
	}
	if tb1 != 4 || gb1 != 92 {
		t.Fatalf("inspect = (%v,%v), want (4,92)", tb1, gb1)
	}
	c.Advance(500 * time.Millisecond) // a: +1, global: +0.5
	tb3, gb3, _, _ := l.Inspect("a")
	if tb3 != 5 || gb3 != 92.5 {
		t.Fatalf("inspect after 0.5s = (%v,%v), want (5,92.5)", tb3, gb3)
	}
	// 查询不消耗：查到的 5 个令牌可以全额取走。
	if err := l.Allow("a", 5); err != nil {
		t.Fatalf("inspected balance must be fully takeable: %v", err)
	}
	tb4, gb4, _, _ := l.Inspect("a")
	if tb4 != 0 || gb4 != 87.5 {
		t.Fatalf("after taking inspected balance = (%v,%v), want (0,87.5)", tb4, gb4)
	}
}
