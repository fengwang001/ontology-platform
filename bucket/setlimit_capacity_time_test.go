package bucket

import (
	"testing"
	"time"
)

// 钉住的语义（任务一.2，缩容截断）：余量因时间流逝补充到某个中间值、但
// 尚未查询入账时缩容，截断发生在下一次 refill：补充照常按速率入账，最终
// 被新容量封顶。调整前应有 8，调整后钉在 7。
// 之前未覆盖：既有 TestSetLimitTruncatesAndNeverTopsUp 的缩容发生在
// 满桶静置状态（无未入账时间），没验证过「时间已流逝但未查询」时缩容。
func TestSetLimitShrinkTruncatesRefilledBalance(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time)
	if !b.TryTake(4) {
		t.Fatal("take 4, balance 6")
	}
	c.Advance(2 * time.Second) // 未入账补充 +2，调整前应有 8
	const wantBefore = 8.0

	b.SetLimit(7, 1) // 容量 10 -> 7，速率不变
	got := b.Balance()

	if !almostEqual(got, 7) {
		t.Fatalf("shrink must truncate refilled balance to new capacity, got %v, want 7", got)
	}
	if !(got < wantBefore) {
		t.Fatalf("truncation must reduce the would-be balance: after=%v before=%v", got, wantBefore)
	}
}

// 钉住的语义（任务一.2，缩容的另一半）：当**已入账**余量本身就超过新容量
// 时，SetLimit 立即把存储余量截到新容量；之后流逝的时间按新容量封顶，
// 不会超过。调整前应有 8（6 已入账 + 2 未入账），调整后钉在 5。
// 之前未覆盖：既有测试的立即截断（10->4）发生在满桶、无未入账时间时。
func TestSetLimitShrinkBelowStoredBalanceTruncatesImmediately(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time)
	if !b.TryTake(4) {
		t.Fatal("take 4, balance 6")
	}
	c.Advance(2 * time.Second) // 未入账 +2，但已入账的 6 已超过新容量 5

	b.SetLimit(5, 1) // 已入账 6 > 5：立即截到 5
	got := b.Balance()

	if !almostEqual(got, 5) {
		t.Fatalf("stored balance above new capacity must be truncated at once, got %v, want 5", got)
	}
	c.Advance(10 * time.Second) // 继续流逝也只能封顶在 5
	if got := b.Balance(); !almostEqual(got, 5) {
		t.Fatalf("refill must stay capped at new capacity, got %v, want 5", got)
	}
}

// 钉住的语义（任务一.2，扩容不凭空补满）：时间已流逝但尚未查询时扩容，
// 余量 = 旧余量 + 流逝时间的补充，不会因为容量变大而被填满。
// 调整前应有 5，调整后仍是 5（只是上限变宽了）。
// 之前未覆盖：既有 grow 断言（1 不变）没有时间流逝的前置状态。
func TestSetLimitGrowKeepsEarnedBalanceOnly(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time)
	if !b.TryTake(8) {
		t.Fatal("take 8, balance 2")
	}
	c.Advance(3 * time.Second) // 未入账 +3，调整前应有 5
	const wantBefore = 5.0

	b.SetLimit(50, 1) // 容量 10 -> 50
	got := b.Balance()

	if !almostEqual(got, wantBefore) {
		t.Fatalf("grow must not top up: got %v, want %v (earned balance only)", got, wantBefore)
	}
	if got >= 50 {
		t.Fatalf("grow must never fill to the new capacity, got %v", got)
	}
}
