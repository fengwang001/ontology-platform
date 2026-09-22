package limiter

import (
	"errors"
	"testing"
	"time"

	"ontology/policy"
)

// 钉住的语义：两级扣减的回滚——拒绝前后租户桶与全局桶的余量逐一相等；
// 租户不够时全局桶完全不被触碰；两种拒绝原因可用 errors.Is / IsXxx 判定；
// 同时不足时报租户原因优先。所有用例都先推进时钟（带补充速率）、再触发
// 拒绝，覆盖"拒绝发生在时间流逝之后"的场景。
// 之前未被覆盖的原因：TestGlobalShortfallLeavesBothBalancesUntouched 与
// TestRejectionCausesDistinguishable 全部使用速率 0 且不推进时钟，回滚路径
// 从未与"拒绝瞬间发生时间补充"叠加验证过（TryTake 内部会先结算补充）。

// 租户够、全局不够：拒绝后两个桶的余量与拒绝前逐一相等（含时间补充）。
func TestGlobalShortfallRollbackAfterRefill(t *testing.T) {
	l, c := newLimiter(t, 10, 1, map[string]policy.Quota{
		"a": policy.Must(10, 2),
		"b": policy.Must(10, 0),
	})
	if err := l.Allow("a", 8); err != nil { // a: 2, global: 2
		t.Fatal(err)
	}
	c.Advance(time.Second) // 结算后 a: 4, global: 3
	tb0, gb0 := balances(t, l, "a")
	if tb0 != 4 || gb0 != 3 {
		t.Fatalf("setup balances = (%v,%v), want (4,3)", tb0, gb0)
	}
	err := l.Allow("b", 5) // b 有 10 够，全局 3 不够
	if !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("want ErrGlobalQuota, got %v", err)
	}
	if !IsGlobalQuota(err) || IsTenantQuota(err) {
		t.Fatalf("IsGlobalQuota/IsTenantQuota mismatch for %v", err)
	}
	tb1, gb1 := balances(t, l, "a")
	if tb1 != tb0 || gb1 != gb0 {
		t.Fatalf("a balances changed on rejection: (%v,%v) -> (%v,%v)", tb0, gb0, tb1, gb1)
	}
	tbB, gbB := balances(t, l, "b")
	if tbB != 10 || gbB != gb0 {
		t.Fatalf("b/global balances changed on rejection: (%v,%v), want (10,%v)", tbB, gbB, gb0)
	}
}

// 租户不够：全局桶一个令牌都不能被动。
func TestTenantShortfallLeavesGlobalUntouched(t *testing.T) {
	l, c := newLimiter(t, 10, 1, map[string]policy.Quota{
		"a": policy.Must(5, 0),
	})
	if err := l.Allow("a", 3); err != nil { // a: 2, global: 7
		t.Fatal(err)
	}
	c.Advance(2 * time.Second) // 全局结算后 9；a 速率 0 仍为 2
	tb0, gb0 := balances(t, l, "a")
	if tb0 != 2 || gb0 != 9 {
		t.Fatalf("setup balances = (%v,%v), want (2,9)", tb0, gb0)
	}
	err := l.Allow("a", 4) // 租户 2 < 4
	if !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("want ErrTenantQuota, got %v", err)
	}
	if !IsTenantQuota(err) || IsGlobalQuota(err) {
		t.Fatalf("IsTenantQuota/IsGlobalQuota mismatch for %v", err)
	}
	tb1, gb1 := balances(t, l, "a")
	if tb1 != tb0 || gb1 != gb0 {
		t.Fatalf("balances changed on tenant rejection: (%v,%v) -> (%v,%v)", tb0, gb0, tb1, gb1)
	}
}

// 租户与全局同时不足（都经过时间补充后）：报租户原因优先。
func TestBothShortAfterRefillTenantCauseWins(t *testing.T) {
	l, c := newLimiter(t, 10, 1, map[string]policy.Quota{
		"a": policy.Must(10, 1),
	})
	if err := l.Allow("a", 8); err != nil { // a: 2, global: 2
		t.Fatal(err)
	}
	c.Advance(time.Second) // 结算后 a: 3, global: 3，都 < 5
	err := l.Allow("a", 5)
	if !errors.Is(err, ErrTenantQuota) || errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("both short: tenant cause must win, got %v", err)
	}
	tb, gb := balances(t, l, "a")
	if tb != 3 || gb != 3 {
		t.Fatalf("rejection must not change balances, got (%v,%v), want (3,3)", tb, gb)
	}
}
