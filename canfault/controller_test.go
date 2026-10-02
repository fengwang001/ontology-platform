package canfault

import (
	"errors"
	"fmt"
	"testing"
)

// logStep 在测试日志中打印输入、输出与判定依据。
func logStep(t *testing.T, label, input string, got error, s Snapshot, reason string) {
	t.Helper()
	t.Logf("%s | 输入=%s | 输出 err=%v | TEC=%d REC=%d state=%s recovering=%v idle=%d opSeq=%d | 判定: %s",
		label, input, errText(got), s.TEC, s.REC, s.State, s.Recovering, s.IdleCount, s.OpSeq, reason)
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

func mustApply(t *testing.T, c *Controller, ev Event, label string) {
	t.Helper()
	before := c.Snapshot()
	if err := c.Apply(ev); err != nil {
		t.Fatalf("%s: Apply(%s) 意外失败: %v", label, ev, err)
	}
	after := c.Snapshot()
	logStep(t, label, "Apply("+ev.String()+")", nil, after,
		fmt.Sprintf("成功操作序号 %d，状态 %s -> %s", after.OpSeq, before.State, after.State))
}

func wantErr(t *testing.T, label string, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: 期望错误 %v，实际 %v", label, want, got)
	}
	t.Logf("%s | 输出 err=%v | 判定: 按预期拒绝（%s）", label, got, want)
}

// TestInitialState 验证初始 TEC=REC=0、主动错误、无迁移记录。
func TestInitialState(t *testing.T) {
	s := New().Snapshot()
	logStep(t, "初始", "New()", nil, s, "TEC=0 REC=0 -> ErrorActive，无迁移记录")
	if s.TEC != 0 || s.REC != 0 || s.State != ErrorActive {
		t.Fatalf("初始状态错误: %+v", s)
	}
	if s.OpSeq != 0 || s.Recovering || s.IdleCount != 0 || len(s.Transitions) != 0 {
		t.Fatalf("初始元数据错误: %+v", s)
	}
}

// TestTEC127Vs128 覆盖 TEC=127 仍主动、TEC=128 进入被动。
func TestTEC127Vs128(t *testing.T) {
	c := New()
	for i := 0; i < 15; i++ { // 15*8 = 120
		mustApply(t, c, TxErr, fmt.Sprintf("TEC127/128 第%d个TxErr", i+1))
	}
	s := c.Snapshot()
	if s.TEC != 120 || s.State != ErrorActive {
		t.Fatalf("15 个 TxErr 后期望 TEC=120 主动，实际 TEC=%d %s", s.TEC, s.State)
	}
	// 120 --TxAckErr(+8)--> 128 进入被动错误。
	mustApply(t, c, TxAckErr, "主动态 TxAckErr 120->128（进入被动）")
	s = c.Snapshot()
	if s.TEC != 128 || s.State != ErrorPassive {
		t.Fatalf("TEC=128 应被动，实际 TEC=%d %s", s.TEC, s.State)
	}
	if len(s.Transitions) != 1 || s.Transitions[0].Old != ErrorActive || s.Transitions[0].New != ErrorPassive {
		t.Fatalf("期望 1 条 active->passive 迁移，实际 %+v", s.Transitions)
	}
	// 128 --TxOK(-1)--> 127 回到主动错误，验证 127 与 128 的状态差异。
	mustApply(t, c, TxOK, "TxOK 128->127（回到主动）")
	s = c.Snapshot()
	if s.TEC != 127 || s.State != ErrorActive {
		t.Fatalf("TEC=127 应主动，实际 TEC=%d %s", s.TEC, s.State)
	}
	if len(s.Transitions) != 2 || s.Transitions[1].Old != ErrorPassive || s.Transitions[1].New != ErrorActive {
		t.Fatalf("期望第 2 条 passive->active 迁移，实际 %+v", s.Transitions)
	}
	if s.Transitions[1].OpSeq != s.OpSeq {
		t.Fatalf("迁移序号 %d 应为当前成功序号 %d", s.Transitions[1].OpSeq, s.OpSeq)
	}
}

// TestTEC255StillPassive 覆盖 TEC 从 247 加到 255 仍被动。
func TestTEC255StillPassive(t *testing.T) {
	c := New()
	// 31 个 TxErr -> TEC=248（第 16 个后已进入被动）。
	for i := 0; i < 31; i++ {
		mustApply(t, c, TxErr, fmt.Sprintf("255场景 第%d个TxErr", i+1))
	}
	before := c.Snapshot()
	if before.TEC != 248 || before.State != ErrorPassive {
		t.Fatalf("准备失败: 期望 TEC=248 被动，实际 TEC=%d %s", before.TEC, before.State)
	}
	// TxOK 在 TEC>0 时减 1 -> 247，仍被动；再 TxErr +8 -> 255，仍被动。
	mustApply(t, c, TxOK, "TxOK 248->247（仍被动）")
	mustApply(t, c, TxErr, "TxErr 247->255（仍被动，不总线关闭）")
	after := c.Snapshot()
	if after.TEC != 255 || after.State != ErrorPassive {
		t.Fatalf("TEC=255 应仍被动，实际 TEC=%d %s", after.TEC, after.State)
	}
	for _, tr := range after.Transitions {
		if tr.New == BusOff {
			t.Fatalf("TEC 到 255 不应出现总线关闭迁移: %+v", tr)
		}
	}
}

// TestTEC248To256BusOff 覆盖 TEC 从 248 加到 256 进入总线关闭。
func TestTEC248To256BusOff(t *testing.T) {
	c := New()
	for i := 0; i < 31; i++ { // 31*8 = 248
		mustApply(t, c, TxErr, fmt.Sprintf("256场景 第%d个TxErr", i+1))
	}
	before := c.Snapshot()
	if before.TEC != 248 || before.State != ErrorPassive {
		t.Fatalf("期望起点 TEC=248 被动，实际 TEC=%d %s", before.TEC, before.State)
	}
	mustApply(t, c, TxErr, "TxErr 248->256（进入总线关闭）")
	s := c.Snapshot()
	if s.TEC != 256 || s.State != BusOff {
		t.Fatalf("期望 TEC=256 总线关闭，实际 TEC=%d %s", s.TEC, s.State)
	}
	last := s.Transitions[len(s.Transitions)-1]
	if last.Old != ErrorPassive || last.New != BusOff || last.OpSeq != s.OpSeq {
		t.Fatalf("期望 passive->busoff 迁移，实际 %+v", last)
	}
}

// TestREC130RxOK 覆盖 REC=130 时 RxOK 置 127 并回到主动（TEC<=127）。
func TestREC130RxOK(t *testing.T) {
	c := New()
	// 16 个 RxErrDominant -> REC=128（被动），再加 2 个 RxErr -> 130。
	for i := 0; i < 16; i++ {
		mustApply(t, c, RxErrDominant, fmt.Sprintf("130场景 第%d个RxErrDominant", i+1))
	}
	mustApply(t, c, RxErr, "RxErr 128->129")
	mustApply(t, c, RxErr, "RxErr 129->130")
	before := c.Snapshot()
	if before.REC != 130 || before.State != ErrorPassive || before.TEC != 0 {
		t.Fatalf("期望 REC=130 TEC=0 被动，实际 REC=%d TEC=%d %s", before.REC, before.TEC, before.State)
	}
	mustApply(t, c, RxOK, "RxOK: REC>127 直接置 127，回到主动")
	after := c.Snapshot()
	if after.REC != 127 {
		t.Fatalf("REC>127 时 RxOK 应置 127，实际 %d", after.REC)
	}
	if after.State != ErrorActive || after.TEC != 0 {
		t.Fatalf("期望主动且 TEC 不变，实际 TEC=%d %s", after.TEC, after.State)
	}
	last := after.Transitions[len(after.Transitions)-1]
	if last.Old != ErrorPassive || last.New != ErrorActive {
		t.Fatalf("期望 passive->active 迁移，实际 %+v", last)
	}
}

// TestREC127RxErr 覆盖 REC=127 时 RxErr ->128 进入被动。
func TestREC127RxErr(t *testing.T) {
	c := New()
	for i := 0; i < 15; i++ { // 15*8 = 120
		mustApply(t, c, RxErrDominant, fmt.Sprintf("127场景 第%d个RxErrDominant", i+1))
	}
	for i := 0; i < 7; i++ { // 120+7 = 127
		mustApply(t, c, RxErr, fmt.Sprintf("127场景 第%d个RxErr", i+1))
	}
	before := c.Snapshot()
	if before.REC != 127 || before.State != ErrorActive {
		t.Fatalf("期望 REC=127 主动，实际 REC=%d %s", before.REC, before.State)
	}
	mustApply(t, c, RxErr, "RxErr 127->128（进入被动）")
	after := c.Snapshot()
	if after.REC != 128 || after.State != ErrorPassive {
		t.Fatalf("期望 REC=128 被动，实际 REC=%d %s", after.REC, after.State)
	}
}

// TestTxAckErrActiveVsPassive 覆盖主动态 +8、被动态计数不变。
func TestTxAckErrActiveVsPassive(t *testing.T) {
	c := New()
	mustApply(t, c, TxAckErr, "主动态 TxAckErr: 0->8")
	if s := c.Snapshot(); s.TEC != 8 || s.State != ErrorActive {
		t.Fatalf("主动态 TxAckErr 应 +8，实际 TEC=%d %s", s.TEC, s.State)
	}

	// 推进到被动：再 15 个 TxErr -> 128。
	for i := 0; i < 15; i++ {
		mustApply(t, c, TxErr, fmt.Sprintf("进入被动 第%d个TxErr", i+1))
	}
	before := c.Snapshot()
	if before.State != ErrorPassive || before.TEC != 128 {
		t.Fatalf("准备失败: TEC=%d %s", before.TEC, before.State)
	}
	mustApply(t, c, TxAckErr, "被动态 TxAckErr: 两个计数都不变")
	after := c.Snapshot()
	if after.TEC != 128 || after.REC != 0 {
		t.Fatalf("被动态 TxAckErr 不应改计数，实际 TEC=%d REC=%d", after.TEC, after.REC)
	}
	if after.State != ErrorPassive {
		t.Fatalf("被动态 TxAckErr 后仍应被动，实际 %s", after.State)
	}
}

// TestTxOKNoUnderflow 覆盖 TEC=0 时 TxOK 不下溢。
func TestTxOKNoUnderflow(t *testing.T) {
	c := New()
	mustApply(t, c, TxOK, "TEC=0 时 TxOK 不减")
	mustApply(t, c, TxOK, "TEC=0 时再次 TxOK 不减")
	s := c.Snapshot()
	if s.TEC != 0 || s.REC != 0 || s.State != ErrorActive {
		t.Fatalf("期望计数保持 0 主动，实际 TEC=%d REC=%d %s", s.TEC, s.REC, s.State)
	}
	if len(s.Transitions) != 0 {
		t.Fatalf("不应产生迁移记录，实际 %d 条", len(s.Transitions))
	}
}
