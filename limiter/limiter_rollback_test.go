package limiter

import (
	"errors"
	"testing"
	"time"

	"ontology/policy"
)

// 钉住的语义：任务一·4 —— 时间流逝后，租户够而全局不够时拒绝，且两个桶
// 的余量在拒绝前后逐一相等（租户扣减被精确回滚，含按速率补出的量）。
// 之前未被覆盖的原因：既有 TestGlobalShortfallLeavesBothBalancesUntouched
// 全程速率 0、时钟未动，回滚路径从未在"有 refill 参与"的状态下验证过。
func TestGlobalShortfallRollbackAfterRefill(t *testing.T) {
	l, c := newLimiter(t, 10, 1, map[string]policy.Quota{"a": policy.Must(8, 5)})
	if err := l.Allow("a", 8); err != nil { // 租户 0，全局 2
		t.Fatal(err)
	}
	c.Advance(3 * time.Second) // 租户 0+5*3=15→封顶 8；全局 2+1*3=5
	tb0, gb0 := balances(t, l, "a")
	if tb0 != 8 || gb0 != 5 {
		t.Fatalf("precondition: tenant=%v global=%v, want 8 and 5", tb0, gb0)
	}
	err := l.Allow("a", 6) // 租户 8 够，全局 5 不够
	if !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("want ErrGlobalQuota, got %v", err)
	}
	if !IsGlobalQuota(err) || IsTenantQuota(err) {
		t.Fatalf("helper predicates: want global-only, got %v", err)
	}
	tb1, gb1 := balances(t, l, "a")
	if tb1 != tb0 {
		t.Fatalf("tenant balance changed on rejection: %v -> %v", tb0, tb1)
	}
	if gb1 != gb0 {
		t.Fatalf("global balance changed on rejection: %v -> %v", gb0, gb1)
	}
}

// 钉住的语义：任务一·4 —— 时间流逝后，租户不够时直接拒绝，全局桶完全
// 不被触碰（既不扣减也不结算 refill，余额逐值相等）。
// 之前未被覆盖的原因：既有测试只在速率 0 下验证租户拒绝，未验证"全局桶
// 有待结算补充时，租户拒绝是否仍然不碰全局桶"。
func TestTenantShortfallLeavesGlobalUntouched(t *testing.T) {
	l, c := newLimiter(t, 10, 2, map[string]policy.Quota{"a": policy.Must(4, 0)})
	if err := l.Allow("a", 4); err != nil { // 租户 0，全局 6
		t.Fatal(err)
	}
	c.Advance(2 * time.Second) // 全局 6+2*2=10（封顶）；租户仍 0
	tb0, gb0 := balances(t, l, "a")
	if tb0 != 0 || gb0 != 10 {
		t.Fatalf("precondition: tenant=%v global=%v, want 0 and 10", tb0, gb0)
	}
	err := l.Allow("a", 1)
	if !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("want ErrTenantQuota, got %v", err)
	}
	if !IsTenantQuota(err) || IsGlobalQuota(err) {
		t.Fatalf("helper predicates: want tenant-only, got %v", err)
	}
	tb1, gb1 := balances(t, l, "a")
	if tb1 != tb0 {
		t.Fatalf("tenant balance changed on rejection: %v -> %v", tb0, tb1)
	}
	if gb1 != gb0 {
		t.Fatalf("global balance touched by tenant-only rejection: %v -> %v", gb0, gb1)
	}
}

// 钉住的语义：任务一·4 —— 两个桶同时不足时，报租户原因优先
// （ErrTenantQuota 可判定，ErrGlobalQuota 不可判定），且两桶余额都不变。
// 之前未被覆盖的原因：既有 TestRejectionCausesDistinguishable 覆盖了
// tenant-first，但全程时钟未动；本测试在时钟推进后复核同一承诺。
func TestBothShortReportsTenantFirstAfterRefill(t *testing.T) {
	l, c := newLimiter(t, 5, 0, map[string]policy.Quota{
		"a": policy.Must(3, 0),
		"b": policy.Must(5, 0),
	})
	if err := l.Allow("a", 3); err != nil { // 租户 a 0，全局 2
		t.Fatal(err)
	}
	if err := l.Allow("b", 2); err != nil { // 全局 0
		t.Fatal(err)
	}
	c.Advance(time.Second) // 速率均 0，两桶仍同时不足
	tb0, gb0 := balances(t, l, "a")
	err := l.Allow("a", 1) // 租户 0<1 且全局 0<1
	if !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("both short: tenant cause must win, got %v", err)
	}
	if errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("both short: global cause must not be reported, got %v", err)
	}
	tb1, gb1 := balances(t, l, "a")
	if tb1 != tb0 || gb1 != gb0 {
		t.Fatalf("balances changed on rejection: (%v,%v) -> (%v,%v)", tb0, gb0, tb1, gb1)
	}
}
