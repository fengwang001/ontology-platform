package bucket

import (
	"testing"
	"time"
)

// 钉住的语义：补充量 = 速率 × 精确的流逝时长（连续值，亚秒级累加），
// 不是按整秒跳变；补到容量后不再增长。
// 之前未被覆盖的原因：TestContinuousRefillAndCap 只验证了 1.5s 单步和
// 长时间后的封顶，没有验证多次非整秒步进的累加结果，也没有验证
// 0.5s / 2.25s 这类边界的连续累加。
func TestFractionalRefillIsContinuous(t *testing.T) {
	c := newFakeClock()
	b := New(10, 2, c.Time) // 2 tokens/sec, cap 10
	if !b.TryTake(10) {     // 抽干
		t.Fatal("drain should succeed")
	}
	steps := []struct {
		advance time.Duration
		want    float64
	}{
		{500 * time.Millisecond, 1},    // 0.5s -> +1
		{1500 * time.Millisecond, 4},   // 1.5s -> +3
		{2250 * time.Millisecond, 8.5}, // 2.25s -> +4.5
	}
	for i, s := range steps {
		c.Advance(s.advance)
		if got := b.Balance(); !almostEqual(got, s.want) {
			t.Fatalf("step %d: advance %v, balance = %v, want %v (continuous, not whole-second)",
				i, s.advance, got, s.want)
		}
	}
}

// 钉住的语义：补充到容量后封顶，继续流逝时间余量不再增长。
// 之前未被覆盖的原因：已有用例只验证了"长时间流逝后等于容量"，
// 没有验证"刚好补到容量的边界"以及"到顶后再推进时钟保持不变"。
func TestRefillStopsExactlyAtCapacity(t *testing.T) {
	c := newFakeClock()
	b := New(10, 2, c.Time)
	if !b.TryTake(10) {
		t.Fatal("drain should succeed")
	}
	c.Advance(2500 * time.Millisecond) // +5
	if got := b.Balance(); !almostEqual(got, 5) {
		t.Fatalf("got %v, want 5", got)
	}
	c.Advance(2500 * time.Millisecond) // +5 -> 刚好到 10
	if got := b.Balance(); !almostEqual(got, 10) {
		t.Fatalf("got %v, want exactly 10 at capacity boundary", got)
	}
	c.Advance(3 * time.Second) // 到顶后不再增长
	if got := b.Balance(); !almostEqual(got, 10) {
		t.Fatalf("balance must stay capped at 10, got %v", got)
	}
}

// 钉住的语义：在"有待补充时间"的状态下时钟回拨，余量既不倒扣也不补充
// （回拨期间视为没有流逝）；时钟推回原处后，之前应得的补充仍然补得上，
// 且不会重复补充。
// 之前未被覆盖的原因：TestClockRewindDoesNotDeduct 在回拨【前】先查询了一次
// （查询已结算并推进了内部时间戳），没有覆盖"补充尚未结算就回拨"的路径，
// 也没有验证时钟恢复后的补账行为。
func TestRewindWithPendingRefill(t *testing.T) {
	c := newFakeClock()
	b := New(10, 5, c.Time)
	if !b.TryTake(6) { // 余量 4，last = T0
		t.Fatal("take 6")
	}
	c.Advance(time.Second) // 待补充 5，未查询
	c.Advance(-30 * time.Minute)
	if got := b.Balance(); !almostEqual(got, 4) {
		t.Fatalf("rewind with pending refill: got %v, want 4 (no deduct, no refill)", got)
	}
	c.Advance(30 * time.Minute) // 推回 T0+1s
	if got := b.Balance(); !almostEqual(got, 9) {
		t.Fatalf("clock restored: got %v, want 9 (pending refill still credited)", got)
	}
	if got := b.Balance(); !almostEqual(got, 9) {
		t.Fatalf("second query must not double-refill, got %v", got)
	}
	c.Advance(-time.Hour) // 再次回拨到已结算时刻之前
	if got := b.Balance(); !almostEqual(got, 9) {
		t.Fatalf("rewind below settled time must not deduct, got %v", got)
	}
	c.Advance(time.Hour) // 回到原处：不重复补充
	if got := b.Balance(); !almostEqual(got, 9) {
		t.Fatalf("returning to settled time must not re-refill, got %v", got)
	}
}
