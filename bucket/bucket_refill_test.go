package bucket

import (
	"testing"
	"time"
)

// 钉住的语义：任务一·3 —— 补充是时间的连续函数：按 0.5s / 1.5s / 2.25s
// 等非整秒步进时，补充量按 rate*dt 精确累计（出现 8.5 这样的非整数值），
// 而不是按整秒跳变；补到容量后不再增长。
// 之前未被覆盖的原因：既有 TestContinuousRefillAndCap 只用 1.5s 单步进，
// 未验证多步非整秒累计的连续性，也未验证"刚好补到容量边界"的步进。
func TestFractionalRefillIsContinuous(t *testing.T) {
	c := newFakeClock()
	b := New(10, 2, c.Time) // 2 tokens/sec, cap 10
	if !b.TryTake(10) {
		t.Fatal("drain bucket")
	}
	steps := []struct {
		advance time.Duration
		want    float64
	}{
		{500 * time.Millisecond, 1},    // +2*0.5
		{1500 * time.Millisecond, 4},   // +2*1.5
		{2250 * time.Millisecond, 8.5}, // +2*2.25，非整数值证明连续补充
		{750 * time.Millisecond, 10},   // +2*0.75，刚好补到容量
		{5 * time.Second, 10},          // 到容量后不再增长
	}
	for i, s := range steps {
		c.Advance(s.advance)
		if got := b.Balance(); !almostEqual(got, s.want) {
			t.Fatalf("step %d (+%v): got %v, want %v", i, s.advance, got, s.want)
		}
	}
}

// 钉住的语义：任务一·5 —— 时钟回拨被忽略：余量不倒扣；且内部时间戳不
// 随回拨倒退，因此把时钟推回原处后不会对已结算的区间重复补充；只有越过
// 原位置之后的新时间才继续产生补充。
// 之前未被覆盖的原因：既有 TestClockRewindDoesNotDeduct 只验证回拨不倒扣，
// 未验证"推回原处不重复补充"以及"越过原位置后按差额补充"。
func TestClockRewindNoDeductNoDoubleRefill(t *testing.T) {
	c := newFakeClock()
	b := New(10, 5, c.Time) // 5 tokens/sec
	if !b.TryTake(8) {
		t.Fatal("take 8, stored 2")
	}
	c.Advance(time.Second) // 结算到 T0+1s：2+5=7
	if got := b.Balance(); !almostEqual(got, 7) {
		t.Fatalf("after 1s got %v, want 7", got)
	}
	// 回拨 30 分钟：不倒扣。
	c.Advance(-30 * time.Minute)
	if got := b.Balance(); !almostEqual(got, 7) {
		t.Fatalf("rewind must not deduct, got %v", got)
	}
	// 部分推回（仍落后于内部时间戳）：不补充。
	c.Advance(29 * time.Minute)
	if got := b.Balance(); !almostEqual(got, 7) {
		t.Fatalf("still before last-settled instant, got %v", got)
	}
	// 推回恰好到原处：不重复补充已结算区间。
	c.Advance(time.Minute)
	if got := b.Balance(); !almostEqual(got, 7) {
		t.Fatalf("restoring clock must not double-refill, got %v", got)
	}
	// 越过原处 0.5s：只补这 0.5s 的差额。
	c.Advance(500 * time.Millisecond)
	if got := b.Balance(); !almostEqual(got, 9.5) {
		t.Fatalf("only time past the settled instant refills, got %v, want 9.5", got)
	}
}
