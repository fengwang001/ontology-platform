package bucket

import (
	"testing"
	"time"
)

// 钉住的语义：任务一·1 —— SetLimit 不结算已流逝时间、不推进内部时间戳，
// 因此"变更前已流逝但未查询的时间"会在下一次 Balance/TryTake 时按**新速率**
// 结算。调大速率时，历史时间会按新速率补进来（实际余量 > 调整前应有余量）。
// 之前未被覆盖的原因：既有 TestSetLimitTruncatesAndNeverTopsUp 只在 SetLimit
// 前后立刻断言，从未在"时间已流逝但未查询"的中间状态下调整速率。
func TestSetLimitRateUpAppliesToElapsedTime(t *testing.T) {
	c := newFakeClock()
	b := New(100, 1, c.Time) // 1 token/sec
	if !b.TryTake(100) {
		t.Fatal("drain bucket")
	}
	c.Advance(6 * time.Second) // 6s 流逝但未查询：按旧速率应得 6
	const wouldBeBefore = 6.0  // 调整前（按旧速率 1/s）应有余量
	b.SetLimit(100, 10)        // 速率调大到 10/s
	got := b.Balance()
	// 当前实现：未结算的 6s 按新速率 10/s 补，得 60。
	if !almostEqual(got, 60) {
		t.Fatalf("after rate-up got %v, want 60", got)
	}
	if got <= wouldBeBefore {
		t.Fatalf("rate-up: actual %v should exceed pre-change would-be %v", got, wouldBeBefore)
	}
}

// 钉住的语义：任务一·1 —— 调小速率时，未结算的历史时间同样按新的低速率
// 重算，实际余量 < 调整前（按旧速率）应有余量。
// 之前未被覆盖的原因：同上，既有测试没有在时间流逝后调小速率。
func TestSetLimitRateDownAppliesToElapsedTime(t *testing.T) {
	c := newFakeClock()
	b := New(100, 10, c.Time) // 10 tokens/sec
	if !b.TryTake(100) {
		t.Fatal("drain bucket")
	}
	c.Advance(6 * time.Second) // 按旧速率应得 60
	const wouldBeBefore = 60.0
	b.SetLimit(100, 1) // 速率调小到 1/s
	got := b.Balance()
	// 当前实现：6s 按新速率 1/s 重算，只得 6。
	if !almostEqual(got, 6) {
		t.Fatalf("after rate-down got %v, want 6", got)
	}
	if got >= wouldBeBefore {
		t.Fatalf("rate-down: actual %v should be below pre-change would-be %v", got, wouldBeBefore)
	}
}

// 钉住的语义：任务一·1 —— 速率改成 0 时，已流逝但未结算的时间所应得的
// 补充被整体丢弃（余量 = 存储的残值 0），而不是先按旧速率结算再冻结。
// 之前未被覆盖的原因：既有测试从未覆盖"调 0"这条运维冻结配额的场景。
func TestSetLimitRateZeroDropsPendingRefill(t *testing.T) {
	c := newFakeClock()
	b := New(100, 5, c.Time)
	if !b.TryTake(100) {
		t.Fatal("drain bucket")
	}
	c.Advance(4 * time.Second) // 按旧速率应得 20
	const wouldBeBefore = 20.0
	b.SetLimit(100, 0) // 冻结补充
	got := b.Balance()
	if !almostEqual(got, 0) {
		t.Fatalf("after rate=0 got %v, want 0 (pending refill dropped)", got)
	}
	if got >= wouldBeBefore {
		t.Fatalf("rate=0: actual %v should be below pre-change would-be %v", got, wouldBeBefore)
	}
	// 冻结后再流逝时间也不再补充。
	c.Advance(time.Hour)
	if got := b.Balance(); !almostEqual(got, 0) {
		t.Fatalf("frozen bucket must stay empty, got %v", got)
	}
}

// 钉住的语义：任务一·2 —— 时间已流逝但未查询时缩容，补充按新容量截断：
// 余量被钉在新容量上，不超过新容量。
// 之前未被覆盖的原因：既有 TestSetLimitTruncatesAndNeverTopsUp 的缩容发生
// 在满桶无流逝时刻，未覆盖"流逝后再缩容"。
func TestSetLimitShrinkAfterElapsedTime(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time)
	if !b.TryTake(4) {
		t.Fatal("take 4, stored 6")
	}
	c.Advance(3 * time.Second) // 按旧容量结算应为 6+3=9
	const wouldBeBefore = 9.0
	b.SetLimit(7, 1) // 缩容到 7
	got := b.Balance()
	if !almostEqual(got, 7) {
		t.Fatalf("shrink after elapsed time got %v, want 7 (new capacity)", got)
	}
	if got >= wouldBeBefore {
		t.Fatalf("shrink: actual %v should be below pre-change would-be %v", got, wouldBeBefore)
	}
}

// 钉住的语义：任务一·2 —— 扩容不凭空补满：残值低于旧容量时，扩容后余量
// 不变（历史时间只补到实际应得，不会补到新容量）。
// 之前未被覆盖的原因：既有测试的扩容断言紧接 TryTake，没有时间流逝参与。
func TestSetLimitGrowDoesNotInventTokens(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time)
	if !b.TryTake(10) {
		t.Fatal("drain bucket")
	}
	c.Advance(5 * time.Second) // 应得 5
	const wouldBeBefore = 5.0
	b.SetLimit(20, 1) // 扩容到 20
	got := b.Balance()
	if !almostEqual(got, 5) {
		t.Fatalf("grow must not invent tokens, got %v, want 5", got)
	}
	if got != wouldBeBefore {
		t.Fatalf("grow: actual %v should equal pre-change would-be %v", got, wouldBeBefore)
	}
}

// 钉住的语义：任务一·2 —— 扩容的边界情形：满桶静置很久（历史时间远超旧
// 容量可吸收的量）再扩容，当前实现会把这段历史时间按**新容量**重新结算，
// 余量因此超过"调整前应有余量"（旧容量）。这是 SetLimit 不结算 refill 的
// 直接后果，测试钉住该行为本身，不评价对错（见 FINDINGS.md）。
// 之前未被覆盖的原因：既有测试从未在"满桶+长时间流逝"后扩容。
func TestSetLimitGrowResettlesHistoryAgainstNewCapacity(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time) // 满桶，stored 10
	c.Advance(100 * time.Second)
	const wouldBeBefore = 10.0 // 调整前查询：补到旧容量 10 封顶
	b.SetLimit(20, 1)          // 扩容到 20
	got := b.Balance()
	// 当前实现：10 + 1*100 = 110，按新容量 20 截断。
	if !almostEqual(got, 20) {
		t.Fatalf("grow after long idle got %v, want 20", got)
	}
	if got <= wouldBeBefore {
		t.Fatalf("grow: actual %v exceeds pre-change would-be %v (history resettled against new capacity)", got, wouldBeBefore)
	}
}
