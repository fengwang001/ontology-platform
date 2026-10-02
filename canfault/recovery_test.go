package canfault

import (
	"fmt"
	"testing"
)

func driveToBusOff(t *testing.T, c *Controller) {
	t.Helper()
	for i := 0; i < 32; i++ { // 32*8 = 256
		mustApply(t, c, TxErr, fmt.Sprintf("进入总线关闭 第%d个TxErr", i+1))
	}
	if s := c.Snapshot(); s.State != BusOff {
		t.Fatalf("准备失败: 期望总线关闭，实际 %s", s.State)
	}
}

// TestRecovery127NotDone128Done 覆盖恢复第 127 次尚未完成、第 128 次完成。
func TestRecovery127NotDone128Done(t *testing.T) {
	c := New()
	driveToBusOff(t, c)
	transBefore := len(c.Snapshot().Transitions)

	if err := c.Restart(); err != nil {
		t.Fatalf("Restart 意外失败: %v", err)
	}
	s := c.Snapshot()
	t.Logf("Restart | TEC=%d REC=%d state=%s recovering=%v idle=%d opSeq=%d | 判定: 开始恢复，恢复计数置 0，状态仍为总线关闭",
		s.TEC, s.REC, s.State, s.Recovering, s.IdleCount, s.OpSeq)
	if !s.Recovering || s.IdleCount != 0 || s.State != BusOff || s.TEC != 256 {
		t.Fatalf("Restart 后状态错误: %+v", s)
	}

	for i := 1; i <= 127; i++ {
		if err := c.Idle11(); err != nil {
			t.Fatalf("第 %d 次 Idle11 意外失败: %v", i, err)
		}
	}
	s = c.Snapshot()
	if !s.Recovering || s.IdleCount != 127 || s.State != BusOff {
		t.Fatalf("第 127 次 Idle11 后应仍在恢复中（idle=127、总线关闭），实际 %+v", s)
	}
	if len(s.Transitions) != transBefore {
		t.Fatalf("恢复未完成时不应追加迁移记录，之前 %d 条现在 %d 条", transBefore, len(s.Transitions))
	}
	t.Logf("第127次 Idle11 | idleCount=%d state=%s | 判定: 尚未达到 128，恢复未完成", s.IdleCount, s.State)

	if err := c.Idle11(); err != nil {
		t.Fatalf("第 128 次 Idle11 意外失败: %v", err)
	}
	s = c.Snapshot()
	logStep(t, "第128次 Idle11", "Idle11()", nil, s, "恢复计数到 128：TEC/REC 清零，回到主动，恢复结束")
	if s.Recovering || s.IdleCount != 0 || s.State != ErrorActive || s.TEC != 0 || s.REC != 0 {
		t.Fatalf("恢复完成后状态错误: %+v", s)
	}
	last := s.Transitions[len(s.Transitions)-1]
	if last.Old != BusOff || last.New != ErrorActive || last.OpSeq != s.OpSeq {
		t.Fatalf("期望 busoff->active 迁移，实际 %+v", last)
	}
}

// TestRestartRejectedWhileRecovering 覆盖恢复中再次 Restart 被拒。
func TestRestartRejectedWhileRecovering(t *testing.T) {
	c := New()
	driveToBusOff(t, c)
	if err := c.Restart(); err != nil {
		t.Fatal(err)
	}
	wantErr(t, "恢复中再次 Restart", c.Restart(), ErrRestartAlreadyRecovering)
	if s := c.Snapshot(); s.IdleCount != 0 || !s.Recovering {
		t.Fatalf("被拒的 Restart 不得改变恢复状态: %+v", s)
	}
}

// TestRestartNotBusOff 覆盖非总线关闭时 Restart 被拒。
func TestRestartNotBusOff(t *testing.T) {
	c := New()
	wantErr(t, "主动态 Restart", c.Restart(), ErrRestartNotBusOff)
	for i := 0; i < 16; i++ { // 16*8 = 128 被动
		mustApply(t, c, TxErr, "推进到被动")
	}
	wantErr(t, "被动态 Restart", c.Restart(), ErrRestartNotBusOff)
}

// TestIdle11NotRecovering 覆盖非恢复中 Idle11 被拒。
func TestIdle11NotRecovering(t *testing.T) {
	c := New()
	wantErr(t, "初始 Idle11", c.Idle11(), ErrIdle11NotRecovering)
}

// TestApplyRejectedBusOff 覆盖总线关闭时 Apply 一律拒绝。
func TestApplyRejectedBusOff(t *testing.T) {
	c := New()
	driveToBusOff(t, c)
	for _, ev := range []Event{TxOK, TxErr, TxAckErr, RxOK, RxErr, RxErrDominant} {
		wantErr(t, "总线关闭 Apply("+ev.String()+")", c.Apply(ev), ErrApplyWhileBusOff)
	}
	s := c.Snapshot()
	if s.TEC != 256 || s.State != BusOff {
		t.Fatalf("被拒 Apply 后状态应不变，实际 TEC=%d %s", s.TEC, s.State)
	}
}

// TestInvalidEventRejectedFirst 覆盖非法事件先于总线关闭判定。
func TestInvalidEventRejectedFirst(t *testing.T) {
	c := New()
	driveToBusOff(t, c)
	for _, ev := range []Event{Event(-1), Event(99)} {
		wantErr(t, "总线关闭下非法事件", c.Apply(ev), ErrInvalidEvent)
	}
	c2 := New()
	wantErr(t, "主动态非法事件", c2.Apply(Event(42)), ErrInvalidEvent)
}

// TestRejectedOpsDoNotConsumeSeq 覆盖被拒操作不占成功序号、不改任何状态。
func TestRejectedOpsDoNotConsumeSeq(t *testing.T) {
	c := New()

	// rejectAndFreeze 执行一次应被拒操作，并校验快照逐字段不变。
	rejectAndFreeze := func(name string, call func() error, want error) {
		before := c.Snapshot()
		got := call()
		after := c.Snapshot()
		wantErr(t, name, got, want)
		if before.TEC != after.TEC || before.REC != after.REC ||
			before.State != after.State || before.Recovering != after.Recovering ||
			before.IdleCount != after.IdleCount || before.OpSeq != after.OpSeq ||
			len(before.Transitions) != len(after.Transitions) {
			t.Fatalf("被拒操作 %s 改变了状态:\nbefore=%+v\nafter =%+v", name, before, after)
		}
	}

	rejectAndFreeze("非法 Apply", func() error { return c.Apply(Event(7)) }, ErrInvalidEvent)
	rejectAndFreeze("主动态 Restart", c.Restart, ErrRestartNotBusOff)
	rejectAndFreeze("非恢复 Idle11", c.Idle11, ErrIdle11NotRecovering)

	driveToBusOff(t, c)
	rejectAndFreeze("总线关闭 Apply", func() error { return c.Apply(TxOK) }, ErrApplyWhileBusOff)
	// 即使已总线关闭，非法事件仍先报 ErrInvalidEvent。
	rejectAndFreeze("总线关闭非法 Apply", func() error { return c.Apply(Event(-3)) }, ErrInvalidEvent)

	if err := c.Restart(); err != nil {
		t.Fatal(err)
	}
	rejectAndFreeze("恢复中 Restart", c.Restart, ErrRestartAlreadyRecovering)
	rejectAndFreeze("恢复中 Apply", func() error { return c.Apply(RxErr) }, ErrApplyWhileBusOff)

	// 再成功 Idle11，确认其序号紧接此前最后一次成功操作。
	seqBefore := c.Snapshot().OpSeq
	if err := c.Idle11(); err != nil {
		t.Fatal(err)
	}
	if s := c.Snapshot(); s.OpSeq != seqBefore+1 {
		t.Fatalf("被拒操作不得占用序号: 期望 %d，实际 %d", seqBefore+1, s.OpSeq)
	}
}

// TestTransitionsChain 覆盖迁移记录首尾衔接。
func TestTransitionsChain(t *testing.T) {
	c := New()
	for i := 0; i < 16; i++ { // 16*8 = 128 进入被动
		mustApply(t, c, TxErr, "迁移链 TxErr")
	}
	mustApply(t, c, TxOK, "迁移链 TxOK 128->127 回到主动")
	s := c.Snapshot()
	want := []Transition{
		{Old: ErrorActive, New: ErrorPassive},
		{Old: ErrorPassive, New: ErrorActive},
	}
	if len(s.Transitions) != len(want) {
		t.Fatalf("期望 %d 条迁移，实际 %d: %+v", len(want), len(s.Transitions), s.Transitions)
	}
	for i, tr := range s.Transitions {
		if tr.Old != want[i].Old || tr.New != want[i].New {
			t.Fatalf("第 %d 条迁移期望 %s->%s，实际 %s->%s", i, want[i].Old, want[i].New, tr.Old, tr.New)
		}
		if i > 0 && s.Transitions[i-1].New != tr.Old {
			t.Fatalf("迁移记录未首尾衔接: %+v", s.Transitions)
		}
		if tr.OpSeq <= 0 {
			t.Fatalf("迁移必须携带成功操作序号: %+v", tr)
		}
	}
}
