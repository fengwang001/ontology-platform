package limiter

import (
	"testing"
	"time"

	"ontology/policy"
)

// 钉住的语义（任务一.1，limiter 层）：SetQuota 热更新发生在「桶已抽干、
// 静置一段时间、尚未查询」之后时，未入账的流逝时间按**新配额速率**结算。
// 调大速率：过去 10s 按 5/s 补发，余量 50，高于按旧速率应得的 10。
// 之前未覆盖：既有 TestHotUpdateQuota 虽推进了 6s，但只验证容量截断，
// 且新旧速率变化（1->2）下未入账时间按新速率结算这一点从未被断言。
func TestSetQuotaRateUpAfterQuietPeriod(t *testing.T) {
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{
		"a": policy.Must(100, 1),
	})
	if err := l.Allow("a", 100); err != nil {
		t.Fatal("drain tenant bucket")
	}
	c.Advance(10 * time.Second) // 按旧速率应得 10，未入账
	const wantBefore = 10.0

	if err := l.SetQuota("a", policy.Must(100, 5)); err != nil {
		t.Fatal(err)
	}
	tb, _ := balances(t, l, "a")
	const wantAfter = 50.0 // 0 + 5*10：过去时间按新速率补发

	if tb != wantAfter {
		t.Fatalf("tenant balance after rate-up = %v, want %v", tb, wantAfter)
	}
	if !(tb > wantBefore) {
		t.Fatalf("rate-up retroactively credits elapsed time: after=%v before=%v", tb, wantBefore)
	}
}

// 钉住的语义（任务一.1，调小与归零）：静置期「应得」的补充在调小速率后
// 缩水（50 -> 10），速率改成 0 后全部蒸发（50 -> 0）。
// 之前未覆盖：既有热更新测试从未把速率调小或归零过。
func TestSetQuotaRateDownAndZeroAfterQuietPeriod(t *testing.T) {
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{
		"a": policy.Must(100, 5),
		"b": policy.Must(100, 5),
	})
	for _, tenant := range []string{"a", "b"} {
		if err := l.Allow(tenant, 100); err != nil {
			t.Fatalf("drain %s: %v", tenant, err)
		}
	}
	c.Advance(10 * time.Second) // 两桶各应得 50，未入账
	const wantBefore = 50.0

	if err := l.SetQuota("a", policy.Must(100, 1)); err != nil { // 调小
		t.Fatal(err)
	}
	if err := l.SetQuota("b", policy.Must(100, 0)); err != nil { // 归零
		t.Fatal(err)
	}

	tba, _ := balances(t, l, "a")
	if tba != 10 { // 0 + 1*10
		t.Fatalf("rate-down: tenant balance = %v, want 10", tba)
	}
	if !(tba < wantBefore) {
		t.Fatalf("rate-down shrinks unclaimed refill: after=%v before=%v", tba, wantBefore)
	}
	tbb, _ := balances(t, l, "b")
	if tbb != 0 { // 0 + 0*10
		t.Fatalf("rate-zero: tenant balance = %v, want 0", tbb)
	}
	if !(tbb < wantBefore) {
		t.Fatalf("rate-zero erases unclaimed refill: after=%v before=%v", tbb, wantBefore)
	}
}

// 钉住的语义（任务一.2，limiter 层）：时间已流逝但尚未查询时缩容，余量
// 被新容量截断（应有 8 -> 7）；扩容不凭空补满（应有 5 -> 仍是 5）。
// 之前未覆盖：既有 TestHotUpdateQuota 的 grow 断言紧接 shrink 之后，
// 中间没有时间流逝，未覆盖「静置后扩容」的形态。
func TestSetQuotaCapacityChangeAfterQuietPeriod(t *testing.T) {
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{
		"a": policy.Must(10, 1),
		"b": policy.Must(10, 1),
	})
	if err := l.Allow("a", 4); err != nil { // a 余 6
		t.Fatal(err)
	}
	if err := l.Allow("b", 8); err != nil { // b 余 2
		t.Fatal(err)
	}
	c.Advance(2 * time.Second) // a 应有 8，b 应有 4，均未入账

	if err := l.SetQuota("a", policy.Must(7, 1)); err != nil { // 缩容 10->7
		t.Fatal(err)
	}
	if err := l.SetQuota("b", policy.Must(50, 1)); err != nil { // 扩容 10->50
		t.Fatal(err)
	}

	tba, _ := balances(t, l, "a")
	if tba != 7 {
		t.Fatalf("shrink must truncate refilled balance to 7, got %v", tba)
	}
	tbb, _ := balances(t, l, "b")
	if tbb != 4 { // 2 + 1*2：只补流逝时间，不填满新容量
		t.Fatalf("grow must not top up: got %v, want 4", tbb)
	}
}
