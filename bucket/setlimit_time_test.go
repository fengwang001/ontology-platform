package bucket

import (
	"testing"
	"time"
)

// 钉住的语义：SetLimit 不结算、不更新内部时间戳，因此"变更前已流逝但尚未
// 被查询的时间"会按【新速率】补账（速率调大）。本文件每个用例都先推进时钟、
// 再调 SetLimit、最后才查询，覆盖"热更新发生在时间流逝之后"的场景。
// 之前未被覆盖的原因：TestSetLimitTruncatesAndNeverTopsUp 只在调整后立即
// 断言（无待结算时间），limiter 层的 TestHotUpdateQuota 虽推进了时钟，但
// 缩容截断把"按旧速率补 6"和"按新速率补 12"两种结果都截成了 4，无法区分
// 速率到底从何时生效。

// 速率调大：抽干后静置 6s（旧速率 2/s 应得 12），调成 5/s 后实际补 30。
func TestSetLimitRateUpAppliesToElapsedTime(t *testing.T) {
	c := newFakeClock()
	b := New(100, 2, c.Time)
	if !b.TryTake(100) { // 抽干，last = T0
		t.Fatal("drain should succeed")
	}
	c.Advance(6 * time.Second) // 静置：按旧速率应得 2*6 = 12
	const wantBeforeAdjust = 12.0
	b.SetLimit(100, 5) // 调大速率，未结算
	got := b.Balance()
	if !almostEqual(got, 30) {
		t.Fatalf("balance after rate-up = %v, want 30 (new rate 5 * 6s)", got)
	}
	if got <= wantBeforeAdjust {
		t.Fatalf("actual %v should exceed pre-adjust owed %v: new rate applies to elapsed time", got, wantBeforeAdjust)
	}
}

// 速率调小：同样静置 6s（应得 12），调成 0.5/s 后实际只剩 3。
func TestSetLimitRateDownAppliesToElapsedTime(t *testing.T) {
	c := newFakeClock()
	b := New(100, 2, c.Time)
	if !b.TryTake(100) {
		t.Fatal("drain should succeed")
	}
	c.Advance(6 * time.Second)
	const wantBeforeAdjust = 12.0
	b.SetLimit(100, 0.5) // 调小速率
	got := b.Balance()
	if !almostEqual(got, 3) {
		t.Fatalf("balance after rate-down = %v, want 3 (new rate 0.5 * 6s)", got)
	}
	if got >= wantBeforeAdjust {
		t.Fatalf("actual %v should be below pre-adjust owed %v: new rate applies retroactively", got, wantBeforeAdjust)
	}
}

// 速率改成 0：静置期间应得的补充被全部抹掉。
func TestSetLimitRateZeroErasesElapsedRefill(t *testing.T) {
	c := newFakeClock()
	b := New(100, 2, c.Time)
	if !b.TryTake(100) {
		t.Fatal("drain should succeed")
	}
	c.Advance(6 * time.Second) // 按旧速率应得 12
	b.SetLimit(100, 0)         // 速率归零
	if got := b.Balance(); !almostEqual(got, 0) {
		t.Fatalf("balance after rate=0 = %v, want 0 (elapsed refill erased)", got)
	}
	c.Advance(10 * time.Second) // 速率 0：不再补充
	if got := b.Balance(); !almostEqual(got, 0) {
		t.Fatalf("rate 0 must never refill, got %v", got)
	}
}

// 缩容截断发生在"时间已流逝但尚未查询"的状态下：
// 存量低于新容量时 SetLimit 不截断，截断由随后 refill 的容量上限完成。
func TestSetLimitShrinkWithPendingRefill(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time)
	if !b.TryTake(10) { // 存量 0
		t.Fatal("drain should succeed")
	}
	c.Advance(6 * time.Second) // 待补充 6，未查询
	b.SetLimit(4, 1)           // 存量 0 < 4，SetLimit 本身不截断
	if got := b.Balance(); !almostEqual(got, 4) {
		t.Fatalf("shrink with pending refill = %v, want 4 (refill capped at new capacity)", got)
	}
}

// 缩容的另一种时序：存量 6、静置 3s（将补到 9）时缩到 7，结果被截到 7。
func TestSetLimitShrinkTruncatesRefilledBalance(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time)
	if !b.TryTake(4) { // 存量 6
		t.Fatal("take 4")
	}
	c.Advance(3 * time.Second) // 未查询，补充后本应为 9
	b.SetLimit(7, 1)           // 存量 6 < 7，不截断
	if got := b.Balance(); !almostEqual(got, 7) {
		t.Fatalf("got %v, want 7 (9 truncated to new capacity)", got)
	}
}

// 扩容不凭空补满：抽干后静置 6s 再扩容到 100，余量只是补充到的 6。
func TestSetLimitGrowDoesNotTopUp(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time)
	if !b.TryTake(10) {
		t.Fatal("drain should succeed")
	}
	c.Advance(6 * time.Second)
	b.SetLimit(100, 1) // 扩容
	if got := b.Balance(); !almostEqual(got, 6) {
		t.Fatalf("grow must not top up, got %v, want 6", got)
	}
}

// 扩容的边界行为：满桶静置 5s（期间容量 10 本应封顶）后扩容到 20，
// 静置期的补充按新容量结算，余量 15 超过了旧容量 10。
func TestSetLimitGrowLetsStaleRefillExceedOldCapacity(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time) // 满桶 10
	c.Advance(5 * time.Second)
	b.SetLimit(20, 1) // 扩容前未结算
	if got := b.Balance(); !almostEqual(got, 15) {
		t.Fatalf("got %v, want 15 (stale refill capped at new capacity, not old 10)", got)
	}
}
