package limiter

import (
	"testing"
	"time"

	"ontology/policy"
)

// 钉住的语义：SetQuota 热更新发生在"时间已流逝但尚未查询"之后时，
// 流逝的时间按【新配额速率】补账（bucket.SetLimit 不结算所致），
// 且 SetQuota 完全不触碰全局桶。
// 之前未被覆盖的原因：TestHotUpdateQuota 虽在 SetQuota 前推进了 6s，
// 但缩容截断把"按旧速率补 6"与"按新速率补 12"都截成了 4，断言无法
// 区分速率从何时生效；也没有覆盖调大、调小、归零三种速率变更。

// 速率调大：抽干静置 6s（旧速率应得 12），调成 5/s 后实际余量 30。
func TestSetQuotaRateUpAfterElapsedTime(t *testing.T) {
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{
		"a": policy.Must(100, 2),
	})
	if err := l.Allow("a", 100); err != nil { // 抽干租户桶
		t.Fatal(err)
	}
	c.Advance(6 * time.Second) // 静置：按旧速率 2/s 应得 12
	const wantBeforeAdjust = 12.0
	if err := l.SetQuota("a", policy.Must(100, 5)); err != nil {
		t.Fatal(err)
	}
	tb, gb := balances(t, l, "a")
	if tb != 30 {
		t.Fatalf("tenant balance after rate-up = %v, want 30 (new rate 5 * 6s)", tb)
	}
	if tb <= wantBeforeAdjust {
		t.Fatalf("actual %v should exceed pre-adjust owed %v", tb, wantBeforeAdjust)
	}
	if gb != 900 {
		t.Fatalf("SetQuota must not touch global bucket, got %v, want 900", gb)
	}
}

// 速率调小与归零：静置期应得的补充按新速率缩水（12 -> 3），
// 速率归零时静置期补充被全部抹掉（12 -> 0）。
func TestSetQuotaRateDownAndZeroAfterElapsedTime(t *testing.T) {
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{
		"down": policy.Must(100, 2),
		"zero": policy.Must(100, 2),
	})
	for _, name := range []string{"down", "zero"} {
		if err := l.Allow(name, 100); err != nil {
			t.Fatal(err)
		}
	}
	c.Advance(6 * time.Second) // 两租户各按旧速率应得 12
	if err := l.SetQuota("down", policy.Must(100, 0.5)); err != nil {
		t.Fatal(err)
	}
	if err := l.SetQuota("zero", policy.Must(100, 0)); err != nil {
		t.Fatal(err)
	}
	if tb, _ := balances(t, l, "down"); tb != 3 {
		t.Fatalf("rate-down: got %v, want 3 (new rate 0.5 * 6s)", tb)
	}
	if tb, _ := balances(t, l, "zero"); tb != 0 {
		t.Fatalf("rate-zero: got %v, want 0 (elapsed refill erased)", tb)
	}
	if _, gb := balances(t, l, "down"); gb != 800 {
		t.Fatalf("global must be untouched by SetQuota, got %v, want 800", gb)
	}
}

// 容量截断（limiter 层）：余量因时间补充到中间值时缩容被截断；
// 扩容不凭空补满。两种都在"时间已流逝但尚未查询"的状态下触发。
// 之前未被覆盖的原因：TestHotUpdateQuota 的扩容断言紧跟在一次查询之后
// （时间戳已结算），没有覆盖"扩容发生在未结算的流逝时间之后"的路径。
func TestSetQuotaShrinkAndGrowAfterElapsedTime(t *testing.T) {
	l, c := newLimiter(t, 1000, 0, map[string]policy.Quota{
		"a": policy.Must(10, 1),
	})
	if err := l.Allow("a", 10); err != nil { // 抽干
		t.Fatal(err)
	}
	c.Advance(6 * time.Second) // 待补充 6，未查询
	if err := l.SetQuota("a", policy.Must(4, 1)); err != nil {
		t.Fatal(err)
	}
	if tb, _ := balances(t, l, "a"); tb != 4 {
		t.Fatalf("shrink with pending refill: got %v, want 4 (capped at new capacity)", tb)
	}
	c.Advance(3 * time.Second) // 满 4 后再静置 3s，未查询
	if err := l.SetQuota("a", policy.Must(50, 1)); err != nil {
		t.Fatal(err)
	}
	if tb, _ := balances(t, l, "a"); tb != 7 {
		t.Fatalf("grow must not top up: got %v, want 7 (4 + 3s*1)", tb)
	}
}
