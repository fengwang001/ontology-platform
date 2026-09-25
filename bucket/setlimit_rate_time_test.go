package bucket

import (
	"testing"
	"time"
)

// 钉住的语义（特征测试，任务一.1）：SetLimit 不先做 refill，因此「已流逝但
// 尚未入账」的时间会在下一次 refill 时按**新速率**结算。调大速率时，过去
// 那段时间会按新（更高）速率补发，余量高于按旧速率应得的量。
// 之前未被覆盖的原因：既有 TestSetLimitTruncatesAndNeverTopsUp 只在
// SetLimit 前后立刻断言，从未在 SetLimit 前推进时钟，碰不到这段
// 「未入账时间按新速率结算」的路径。
func TestSetLimitRateUpAppliesNewRateToElapsedTime(t *testing.T) {
	c := newFakeClock()
	b := New(100, 1, c.Time) // 1 token/sec, cap 100
	if !b.TryTake(100) {
		t.Fatal("drain full bucket")
	}
	c.Advance(10 * time.Second) // 按旧速率应得 10，但尚未查询入账
	const wantBefore = 10.0     // 调整前（若先查询）应有的余量：0 + 1*10

	b.SetLimit(100, 5) // 只调速率：1 -> 5
	got := b.Balance() // 实际：0 + 5*10 = 50，过去 10s 按新速率补发
	const wantAfter = 50.0

	if !almostEqual(got, wantAfter) {
		t.Fatalf("balance after rate-up = %v, want %v", got, wantAfter)
	}
	if !(got > wantBefore) {
		t.Fatalf("rate-up must retroactively credit elapsed time at the new rate: after=%v before=%v", got, wantBefore)
	}
}

// 钉住的语义（任务一.1，调小速率）：同上，已流逝未入账的时间按调小后的
// 新速率结算，余量低于按旧速率应得的量——相当于「蒸发」了部分已赚补充。
// 之前未覆盖：既有测试从未在 SetLimit 前让时钟流逝。
func TestSetLimitRateDownAppliesNewRateToElapsedTime(t *testing.T) {
	c := newFakeClock()
	b := New(100, 5, c.Time) // 5 tokens/sec
	if !b.TryTake(100) {
		t.Fatal("drain full bucket")
	}
	c.Advance(10 * time.Second) // 按旧速率应得 50，未入账
	const wantBefore = 50.0

	b.SetLimit(100, 1) // 5 -> 1
	got := b.Balance() // 实际：0 + 1*10 = 10
	const wantAfter = 10.0

	if !almostEqual(got, wantAfter) {
		t.Fatalf("balance after rate-down = %v, want %v", got, wantAfter)
	}
	if !(got < wantBefore) {
		t.Fatalf("rate-down must retroactively shrink credit for elapsed time: after=%v before=%v", got, wantBefore)
	}
}

// 钉住的语义（任务一.1，速率改成 0）：已流逝未入账的时间按新速率 0 结算，
// 静置期间「应得」的补充全部归零。
// 之前未覆盖：同上，既有 SetLimit 测试没有时间流逝的前置状态。
func TestSetLimitRateZeroErasesUnclaimedRefill(t *testing.T) {
	c := newFakeClock()
	b := New(100, 5, c.Time)
	if !b.TryTake(100) {
		t.Fatal("drain full bucket")
	}
	c.Advance(10 * time.Second) // 按旧速率应得 50，未入账
	const wantBefore = 50.0

	b.SetLimit(100, 0) // 速率归零
	got := b.Balance() // 实际：0 + 0*10 = 0

	if !almostEqual(got, 0) {
		t.Fatalf("balance after rate=0 = %v, want 0", got)
	}
	if !(got < wantBefore) {
		t.Fatalf("earned-but-unclaimed refill must vanish: after=%v before=%v", got, wantBefore)
	}
	// 速率 0 之后时间继续流逝也不再补充。
	c.Advance(time.Hour)
	if got := b.Balance(); !almostEqual(got, 0) {
		t.Fatalf("zero rate must never refill, got %v", got)
	}
}
