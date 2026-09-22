package limiter

import (
	"testing"
	"time"

	"ontology/policy"
)

// 钉住的语义：任务一·1（limiter 层）—— 租户桶抽干并静置一段时间后调大
// 速率，"变更前已流逝但未结算的时间"按新速率补进租户桶：实际余量大于
// 调整前（按旧速率）应有余量。全局桶不受 SetQuota 影响。
// 之前未被覆盖的原因：既有 TestHotUpdateQuota 的断言虽在 6s 流逝后查询，
// 但只覆盖"缩容截断"，从未覆盖调大/调小/调 0 速率这三种灰度场景。
func TestSetQuotaRateUpAfterIdle(t *testing.T) {
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{"a": policy.Must(100, 1)})
	if err := l.Allow("a", 100); err != nil {
		t.Fatal("drain tenant")
	}
	c.Advance(6 * time.Second) // 按旧速率 1/s 应得 6
	const wouldBeBefore = 6.0
	if err := l.SetQuota("a", policy.Must(100, 10)); err != nil {
		t.Fatal(err)
	}
	tb, gb := balances(t, l, "a")
	// 当前实现：未结算的 6s 按新速率 10/s 补，得 60。
	if tb != 60 {
		t.Fatalf("tenant balance after rate-up = %v, want 60", tb)
	}
	if tb <= wouldBeBefore {
		t.Fatalf("rate-up: actual %v should exceed pre-change would-be %v", tb, wouldBeBefore)
	}
	if gb != 900 {
		t.Fatalf("SetQuota must not touch global bucket, got %v, want 900", gb)
	}
}

// 钉住的语义：任务一·1（limiter 层）—— 静置后调小速率，未结算的历史
// 时间按新的低速率重算，实际余量小于调整前应有余量。
// 之前未被覆盖的原因：同上，灰度缩容路径无测试。
func TestSetQuotaRateDownAfterIdle(t *testing.T) {
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{"a": policy.Must(100, 10)})
	if err := l.Allow("a", 100); err != nil {
		t.Fatal("drain tenant")
	}
	c.Advance(6 * time.Second) // 按旧速率 10/s 应得 60
	const wouldBeBefore = 60.0
	if err := l.SetQuota("a", policy.Must(100, 1)); err != nil {
		t.Fatal(err)
	}
	tb, _ := balances(t, l, "a")
	if tb != 6 {
		t.Fatalf("tenant balance after rate-down = %v, want 6", tb)
	}
	if tb >= wouldBeBefore {
		t.Fatalf("rate-down: actual %v should be below pre-change would-be %v", tb, wouldBeBefore)
	}
}

// 钉住的语义：任务一·1（limiter 层）—— 静置后把速率调 0（冻结），已
// 流逝未结算时间应得的补充被整体丢弃，余量只剩存储残值。
// 之前未被覆盖的原因：调 0 冻结路径无任何测试。
func TestSetQuotaRateZeroAfterIdle(t *testing.T) {
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{"a": policy.Must(100, 5)})
	if err := l.Allow("a", 100); err != nil {
		t.Fatal("drain tenant")
	}
	c.Advance(4 * time.Second) // 按旧速率 5/s 应得 20
	const wouldBeBefore = 20.0
	if err := l.SetQuota("a", policy.Must(100, 0)); err != nil {
		t.Fatal(err)
	}
	tb, _ := balances(t, l, "a")
	if tb != 0 {
		t.Fatalf("tenant balance after freeze = %v, want 0 (pending refill dropped)", tb)
	}
	if tb >= wouldBeBefore {
		t.Fatalf("freeze: actual %v should be below pre-change would-be %v", tb, wouldBeBefore)
	}
	c.Advance(time.Hour) // 冻结后时间继续流逝也不再补充
	if tb, _ := balances(t, l, "a"); tb != 0 {
		t.Fatalf("frozen tenant must stay empty, got %v", tb)
	}
}

// 钉住的语义：任务一·2（limiter 层）—— 时间已流逝但未查询时缩容，租户
// 余量被截断到新容量。
// 之前未被覆盖的原因：既有 TestHotUpdateQuota 的缩容前余额恰好为 0，
// 截断路径（stored > newCap）实际未被执行到。
func TestSetQuotaShrinkTruncatesAfterIdle(t *testing.T) {
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{"a": policy.Must(10, 1)})
	// 满桶 10，静置 3s（流逝但未查询），调整前查询应为 10（封顶）。
	c.Advance(3 * time.Second)
	const wouldBeBefore = 10.0
	if err := l.SetQuota("a", policy.Must(7, 1)); err != nil {
		t.Fatal(err)
	}
	tb, _ := balances(t, l, "a")
	if tb != 7 {
		t.Fatalf("shrink must truncate to new capacity 7, got %v", tb)
	}
	if tb >= wouldBeBefore {
		t.Fatalf("shrink: actual %v should be below pre-change would-be %v", tb, wouldBeBefore)
	}
}

// 钉住的语义：任务一·2（limiter 层）—— 时间已流逝但未查询时扩容：残值
// 低于旧容量时不凭空补满；但满桶静置很久再扩容时，当前实现会把历史时间
// 按新容量重新结算，余量超过调整前应有余量（钉住行为本身，见 FINDINGS.md）。
// 之前未被覆盖的原因：既有测试的扩容断言前余额为 4 且无额外流逝，未覆盖
// "满桶+长静置+扩容"这条灰度扩容路径。
func TestSetQuotaGrowAfterIdle(t *testing.T) {
	// 情形一：残值低于旧容量，扩容不凭空补满。
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{"a": policy.Must(10, 1)})
	if err := l.Allow("a", 10); err != nil {
		t.Fatal("drain tenant")
	}
	c.Advance(5 * time.Second) // 应得 5
	if err := l.SetQuota("a", policy.Must(20, 1)); err != nil {
		t.Fatal(err)
	}
	if tb, _ := balances(t, l, "a"); tb != 5 {
		t.Fatalf("grow must not invent tokens, got %v, want 5", tb)
	}

	// 情形二：满桶长静置后扩容，历史时间按新容量重新结算。
	l2, c2 := newLimiter(t, 1000, 0, map[string]policy.Quota{"b": policy.Must(10, 1)})
	c2.Advance(100 * time.Second)
	const wouldBeBefore = 10.0 // 调整前查询：旧容量 10 封顶
	if err := l2.SetQuota("b", policy.Must(20, 1)); err != nil {
		t.Fatal(err)
	}
	tb, _ := balances(t, l2, "b")
	if tb != 20 {
		t.Fatalf("grow after long idle = %v, want 20", tb)
	}
	if tb <= wouldBeBefore {
		t.Fatalf("grow: actual %v exceeds pre-change would-be %v (history resettled against new capacity)", tb, wouldBeBefore)
	}
}
