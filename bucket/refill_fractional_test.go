package bucket

import (
	"testing"
	"time"
)

// 钉住的语义（任务一.3）：补充量 = rate × 流逝秒数，是**连续值**，可以出现
// 0.5、4.25 这类非整数余量，不存在按整秒取整的跳变；补到容量后不再增长。
// 之前未覆盖：既有 TestContinuousRefillAndCap 只推进 1.5s（恰好整出 3 个
// token）和 10s（直接顶满），无法区分「连续补充」与「整秒跳变」两种实现。
func TestFractionalRefillIsContinuous(t *testing.T) {
	c := newFakeClock()
	b := New(10, 1, c.Time) // 1 token/sec
	if !b.TryTake(10) {
		t.Fatal("drain full bucket")
	}

	steps := []struct {
		advance time.Duration
		want    float64
	}{
		{500 * time.Millisecond, 0.5},   // 半秒 -> 半个 token，非整数
		{1500 * time.Millisecond, 2.0},  // 累计 2s
		{2250 * time.Millisecond, 4.25}, // 累计 4.25s -> 4.25，带小数
		{250 * time.Millisecond, 4.5},   // 累计 4.5s
	}
	elapsed := time.Duration(0)
	for i, s := range steps {
		c.Advance(s.advance)
		elapsed += s.advance
		got := b.Balance()
		if !almostEqual(got, s.want) {
			t.Fatalf("step %d: after %v total, balance = %v, want %v", i, elapsed, got, s.want)
		}
		// 钉住「连续」：非整秒推进必须产生非整数余量，而非取整。
		if s.want != float64(int(s.want)) && almostEqual(got, float64(int(got))) {
			t.Fatalf("step %d: refill looks integer-quantized: %v", i, got)
		}
	}
}

// 钉住的语义（任务一.3 的后半）：连续补充到容量后封顶，继续流逝时间余量
// 不再增长，且封顶值恰好等于容量（不多不少）。
// 之前未覆盖：既有封顶断言只查了一次 10s 后的值，没验证「刚到容量」与
// 「超过容量后继续流逝」两个阶段的连续性。
func TestFractionalRefillCapsExactlyAtCapacity(t *testing.T) {
	c := newFakeClock()
	b := New(4, 2, c.Time) // 2 tokens/sec, cap 4
	if !b.TryTake(4) {
		t.Fatal("drain full bucket")
	}

	c.Advance(1500 * time.Millisecond) // +3 -> 3，还差 1 到顶
	if got := b.Balance(); !almostEqual(got, 3) {
		t.Fatalf("balance = %v, want 3", got)
	}
	c.Advance(500 * time.Millisecond) // +1 -> 恰好 4，到顶
	if got := b.Balance(); !almostEqual(got, 4) {
		t.Fatalf("balance = %v, want exactly capacity 4", got)
	}
	c.Advance(250 * time.Millisecond) // +0.5 被截断，仍是 4
	if got := b.Balance(); !almostEqual(got, 4) {
		t.Fatalf("balance must stay capped at 4, got %v", got)
	}
	c.Advance(time.Hour) // 长时间流逝也不越界
	if got := b.Balance(); !almostEqual(got, 4) {
		t.Fatalf("balance must never exceed capacity, got %v", got)
	}
}
