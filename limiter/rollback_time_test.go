package limiter

import (
	"errors"
	"testing"
	"time"

	"ontology/policy"
)

// 钉住的语义（任务一.4，租户够、全局不够）：拒绝发生后，租户桶与全局桶
// 的余量都与拒绝前逐一相等（租户侧扣减被 Refund 回滚）。与既有测试不同，
// 这里拒绝发生在「时钟已推进、两桶都靠时间补充过」的状态下，回滚后的
// 余量必须等于补充后的值，而不是某个旧快照。
// 之前未覆盖：既有 TestGlobalShortfallLeavesBothBalancesUntouched 全程
// 速率 0、时钟未动，回滚路径从未与时间补充叠加验证过。
func TestGlobalShortfallRollbackAfterRefill(t *testing.T) {
	l, c := newLimiter(t, 10, 1, map[string]policy.Quota{
		"a": policy.Must(10, 5), // 租户补得快，全局补得慢
	})
	if err := l.Allow("a", 10); err != nil { // 租户 0，全局 0
		t.Fatal(err)
	}
	c.Advance(4 * time.Second) // 租户 0+20 封顶 10，全局 0+4=4

	tb0, gb0 := balances(t, l, "a")
	if tb0 != 10 || gb0 != 4 {
		t.Fatalf("pre-reject balances = (%v,%v), want (10,4)", tb0, gb0)
	}

	err := l.Allow("a", 6) // 租户够（10>=6），全局不够（4<6）
	if !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("want ErrGlobalQuota, got %v", err)
	}
	if errors.Is(err, ErrTenantQuota) {
		t.Fatalf("tenant cause must not be reported, got %v", err)
	}

	tb1, gb1 := balances(t, l, "a")
	if tb1 != tb0 || gb1 != gb0 {
		t.Fatalf("rejection must leave both balances untouched: (%v,%v) -> (%v,%v)", tb0, gb0, tb1, gb1)
	}
}

// 钉住的语义（任务一.4，租户不够）：租户侧拒绝时全局桶一个 token 都不能
// 被动到——拒绝前后全局余量逐一相等。
// 之前未覆盖：既有测试只验证过「全局不够时两桶都不动」，反向（租户不够
// 时全局不动）只在速率 0 场景下被隐含覆盖，未在时间流逝后逐一核对。
func TestTenantShortfallNeverTouchesGlobal(t *testing.T) {
	l, c := newLimiter(t, 100, 10, map[string]policy.Quota{
		"a": policy.Must(10, 1), // 租户补得慢，全局补得快
	})
	if err := l.Allow("a", 10); err != nil { // 租户 0，全局 90
		t.Fatal(err)
	}
	c.Advance(2 * time.Second) // 租户 0+2=2，全局 90+20 封顶 100

	tb0, gb0 := balances(t, l, "a")
	if tb0 != 2 || gb0 != 100 {
		t.Fatalf("pre-reject balances = (%v,%v), want (2,100)", tb0, gb0)
	}

	err := l.Allow("a", 5) // 租户不够（2<5）
	if !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("want ErrTenantQuota, got %v", err)
	}
	if errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("global cause must not be reported, got %v", err)
	}

	tb1, gb1 := balances(t, l, "a")
	if tb1 != tb0 || gb1 != gb0 {
		t.Fatalf("tenant rejection must not touch either bucket: (%v,%v) -> (%v,%v)", tb0, gb0, tb1, gb1)
	}
}

// 钉住的语义（任务一.4，同时不足）：租户与全局都不够时，错误必须可判定
// 为租户原因（ErrTenantQuota 优先），且两桶余量都保持拒绝前的值。
// 之前未覆盖：既有 TestRejectionCausesDistinguishable 的「同时不足」场景
// 速率全 0、时钟未动，未在时间补充之后验证优先级与余额不变性。
func TestBothShortReportsTenantFirstAfterRefill(t *testing.T) {
	l, c := newLimiter(t, 10, 1, map[string]policy.Quota{
		"a": policy.Must(10, 1),
	})
	if err := l.Allow("a", 10); err != nil { // 两桶都 0
		t.Fatal(err)
	}
	c.Advance(2 * time.Second) // 两桶各 +2

	tb0, gb0 := balances(t, l, "a")
	if tb0 != 2 || gb0 != 2 {
		t.Fatalf("pre-reject balances = (%v,%v), want (2,2)", tb0, gb0)
	}

	err := l.Allow("a", 5) // 租户 2<5 且全局 2<5：同时不足
	if !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("both short: tenant cause must win, got %v", err)
	}
	if errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("global cause must not leak into the error, got %v", err)
	}

	tb1, gb1 := balances(t, l, "a")
	if tb1 != tb0 || gb1 != gb0 {
		t.Fatalf("rejection must not move balances: (%v,%v) -> (%v,%v)", tb0, gb0, tb1, gb1)
	}
}
