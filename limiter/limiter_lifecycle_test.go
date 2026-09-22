package limiter

import (
	"testing"
	"time"

	"ontology/policy"
)

// 钉住的语义：任务一·6 —— 注销时桶里若有因时间流逝应得但尚未结算的
// 补充，这部分不随注销/重建传递：重建得到的是恰好满的新桶，且新桶的
// 补充计时从重建时刻起算（旧桶的时间戳不泄漏）；注销不触碰全局桶。
// 之前未被覆盖的原因：既有 TestUnregisterAndRecreate 全程速率 0、时钟
// 未动，"有待结算补充时注销"这条路径从未验证。
func TestUnregisterDropsPendingRefill(t *testing.T) {
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{"a": policy.Must(100, 2)})
	if err := l.Allow("a", 100); err != nil { // 租户 0，全局 900
		t.Fatal(err)
	}
	c.Advance(5 * time.Second) // 旧桶应得 2*5=10 的补充（未结算）
	if err := l.Unregister("a"); err != nil {
		t.Fatal(err)
	}
	if err := l.Register("a", policy.Must(100, 2)); err != nil {
		t.Fatal(err)
	}
	tb, gb := balances(t, l, "a")
	if tb != 100 {
		t.Fatalf("re-registered tenant must be a fresh full bucket, got %v", tb)
	}
	if gb != 900 {
		t.Fatalf("unregister/register must not touch global, got %v, want 900", gb)
	}
	// 抽干新桶再静置：补充从重建时刻起算。若旧桶时间戳（T0）泄漏，
	// 这里会补 2*10=20 而非 2*5=10。
	if err := l.Allow("a", 100); err != nil {
		t.Fatal(err)
	}
	c.Advance(5 * time.Second)
	if tb, _ := balances(t, l, "a"); tb != 10 {
		t.Fatalf("refill must count from re-registration, got %v, want 10", tb)
	}
}

// 钉住的语义：任务一·7 —— Inspect 是纯粹的：同一时刻连续查询结果完全
// 相同；查询不消耗令牌（查多次之后仍能按查询到的余量全额放行）；查询
// 之间推进时钟，结果按速率精确变化。
// 之前未被覆盖的原因：既有 TestInspectIsPureAndStable 验证了重复查询一致，
// 但未验证"查询不消耗"（查完再 Allow）与"推进时钟后按速率变化"两条。
func TestInspectPurityAndRateChange(t *testing.T) {
	l, c := newLimiter(t, 10, 1, map[string]policy.Quota{"a": policy.Must(8, 2)})
	if err := l.Allow("a", 3); err != nil { // 租户 5，全局 7
		t.Fatal(err)
	}
	c.Advance(time.Second) // 租户 5+2=7，全局 7+1=8
	tb1, gb1, _, _ := l.Inspect("a")
	tb2, gb2, _, _ := l.Inspect("a")
	if tb1 != tb2 || gb1 != gb2 {
		t.Fatalf("same-instant inspects differ: (%v,%v) vs (%v,%v)", tb1, gb1, tb2, gb2)
	}
	if tb1 != 7 || gb1 != 8 {
		t.Fatalf("inspect values: tenant=%v global=%v, want 7 and 8", tb1, gb1)
	}
	// 再查三次也不消耗：之后仍能按查到的余量全额放行。
	for i := 0; i < 3; i++ {
		l.Inspect("a")
	}
	if err := l.Allow("a", 7); err != nil {
		t.Fatalf("inspect must not consume tokens: %v", err)
	}
	// 推进 0.5s：两桶按各自速率变化（租户 0+2*0.5=1，全局 1+1*0.5=1.5）。
	c.Advance(500 * time.Millisecond)
	tb3, gb3, _, _ := l.Inspect("a")
	if tb3 != 1 || gb3 != 1.5 {
		t.Fatalf("inspect after 0.5s: tenant=%v global=%v, want 1 and 1.5", tb3, gb3)
	}
}
