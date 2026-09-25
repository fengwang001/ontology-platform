package bucket

import (
	"testing"
	"time"
)

// 钉住的语义（任务一.5）：在「已流逝时间已入账」的状态下把时钟往回拨，
// 余量既不倒扣也不重复补充；把时钟推回回拨前的位置后，余量与回拨前一致；
// 只有超过原位置之后的新时间才继续补充。
// 之前未覆盖：既有 TestClockRewindDoesNotDeduct 的回拨发生在满桶状态
// （补充被容量封顶，倒扣与不倒扣无法区分），没验证过「桶里有明确的
// 待补充/已补充量」时回拨与回拨恢复的行为。
func TestRewindWithAccruedRefill(t *testing.T) {
	c := newFakeClock()
	b := New(10, 2, c.Time) // 2 tokens/sec
	if !b.TryTake(10) {
		t.Fatal("drain full bucket")
	}

	c.Advance(3 * time.Second) // +6，入账
	if got := b.Balance(); !almostEqual(got, 6) {
		t.Fatalf("balance after 3s = %v, want 6", got)
	}

	c.Advance(-2 * time.Second) // 回拨 2s：不倒扣
	if got := b.Balance(); !almostEqual(got, 6) {
		t.Fatalf("rewind must not deduct, got %v, want 6", got)
	}

	c.Advance(-time.Hour) // 回拨到更早：依然不倒扣
	if got := b.Balance(); !almostEqual(got, 6) {
		t.Fatalf("long rewind must not deduct, got %v, want 6", got)
	}

	c.Advance(time.Hour + 2*time.Second) // 推回回拨前的位置：不重复补充
	if got := b.Balance(); !almostEqual(got, 6) {
		t.Fatalf("returning to pre-rewind time must not double-refill, got %v, want 6", got)
	}

	c.Advance(time.Second) // 超过原位置 1s：+2
	if got := b.Balance(); !almostEqual(got, 8) {
		t.Fatalf("only time beyond the pre-rewind point refills, got %v, want 8", got)
	}
}

// 钉住的语义（任务一.5 的另一形态）：回拨发生在「已流逝但尚未入账」的
// 状态下。此时最后一次入账点仍是回拨前的旧时刻，回拨后查询按回拨后的
// 时钟结算（余量体现为较短的流逝），且回拨区间不会被二次结算。
// 之前未覆盖：既有回拨测试在回拨前已查询入账，没碰过 last 仍停留在
// 旧时刻、时钟却先走后退的窗口。
func TestRewindBeforeAnyQuerySettlesAtRewoundClock(t *testing.T) {
	c := newFakeClock()
	b := New(10, 2, c.Time)
	if !b.TryTake(10) {
		t.Fatal("drain full bucket")
	}

	c.Advance(3 * time.Second)  // 未入账 +6
	c.Advance(-1 * time.Second) // 回拨 1s：查询按 t0+2s 结算 -> 4
	if got := b.Balance(); !almostEqual(got, 4) {
		t.Fatalf("balance settles at rewound clock, got %v, want 4", got)
	}
	c.Advance(time.Second) // 回到 t0+3s：相对入账点 t0+2s 只 +1s -> 6
	if got := b.Balance(); !almostEqual(got, 6) {
		t.Fatalf("refill resumes from the settled point, got %v, want 6", got)
	}
}
