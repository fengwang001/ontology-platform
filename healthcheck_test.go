package ontology

import (
	"errors"
	"sync"
	"testing"
)

func mustNew(t *testing.T, n, r, f, i, fi, di int64, init bool, wf, q int64) *HealthChecker {
	t.Helper()
	hc, err := NewHealthChecker(n, r, f, i, fi, di, init, wf, q)
	if err != nil {
		t.Fatalf("NewHealthChecker unexpected error: %v", err)
	}
	return hc
}

func stateOf(t *testing.T, hc *HealthChecker, id int) TargetState {
	t.Helper()
	s, err := hc.State(id)
	if err != nil {
		t.Fatalf("State(%d) unexpected error: %v", id, err)
	}
	return s
}

func mustProbe(t *testing.T, hc *HealthChecker, id int, ok bool, now int64) {
	t.Helper()
	if err := hc.Probe(id, ok, now); err != nil {
		t.Fatalf("Probe(%d,%v,%d) unexpected error: %v", id, ok, now, err)
	}
}

// 题目给出的完整示例。
func TestWorkedExample(t *testing.T) {
	hc := mustNew(t, 2, 2, 3, 10, 3, 20, true, 100, 1)

	mustProbe(t, hc, 0, false, 0)
	s0 := stateOf(t, hc, 0)
	if !s0.Healthy || s0.B != 1 || s0.ND != 3 || s0.FU != 3 {
		t.Fatalf("t0 fail: got %+v", s0)
	}

	mustProbe(t, hc, 1, false, 1)
	s1 := stateOf(t, hc, 1)
	if !s1.Healthy || s1.B != 1 || s1.ND != 11 || s1.FU != 0 {
		t.Fatalf("t1 fail: got %+v", s1)
	}

	mustProbe(t, hc, 0, false, 3)
	s0 = stateOf(t, hc, 0)
	if s0.B != 2 || s0.ND != 6 || s0.FU != 6 {
		t.Fatalf("t3 fail: got %+v", s0)
	}

	mustProbe(t, hc, 0, false, 6)
	s0 = stateOf(t, hc, 0)
	if s0.Healthy || s0.A != 0 || s0.B != 0 || s0.ND != 26 ||
		len(s0.TR) != 1 || s0.TR[0] != 6 {
		t.Fatalf("t6 transition: got %+v", s0)
	}

	mustProbe(t, hc, 0, true, 26)
	s0 = stateOf(t, hc, 0)
	if s0.Healthy || s0.A != 1 || s0.ND != 29 || s0.FU != 29 {
		t.Fatalf("t26 success: got %+v", s0)
	}

	mustProbe(t, hc, 0, true, 29)
	mustProbe(t, hc, 0, true, 32)
	s0 = stateOf(t, hc, 0)
	if s0.Healthy || s0.A != 3 {
		t.Fatalf("before recovery: got %+v", s0)
	}

	mustProbe(t, hc, 0, true, 35)
	s0 = stateOf(t, hc, 0)
	if !s0.Healthy || s0.A != 0 || s0.B != 0 || s0.ND != 45 || s0.FU != 0 ||
		len(s0.TR) != 2 || s0.TR[0] != 6 || s0.TR[1] != 35 {
		t.Fatalf("t35 recovery: got %+v", s0)
	}

	if got := hc.Healthy(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("Healthy = %v", got)
	}
}

// R=1 与 F=1：一次成功 / 失败即转移。
func TestR1F1SingleStepTransitions(t *testing.T) {
	hc := mustNew(t, 1, 1, 1, 10, 3, 20, true, 100, 1)
	mustProbe(t, hc, 0, false, 0)
	s := stateOf(t, hc, 0)
	if s.Healthy || s.ND != 20 || len(s.TR) != 1 || s.TR[0] != 0 {
		t.Fatalf("F=1 transition: %+v", s)
	}
	// 转移已过期后（t+Wf == now 即失效），g=0，Reff=1，一次成功恢复。
	mustProbe(t, hc, 0, true, 100)
	s = stateOf(t, hc, 0)
	if !s.Healthy || s.A != 0 || s.B != 0 || s.ND != 110 || len(s.TR) != 2 {
		t.Fatalf("R=1 recovery: %+v", s)
	}
}

// 健康态的成功清 b，打断失败累计。
func TestHealthySuccessResetsB(t *testing.T) {
	hc := mustNew(t, 1, 1, 3, 10, 3, 20, true, 100, 1)
	mustProbe(t, hc, 0, false, 0)
	mustProbe(t, hc, 0, false, 3)
	mustProbe(t, hc, 0, true, 6) // 清 b，健康 b=0 取 I
	s := stateOf(t, hc, 0)
	if !s.Healthy || s.B != 0 || s.ND != 16 {
		t.Fatalf("success reset b: %+v", s)
	}
	mustProbe(t, hc, 0, false, 16)
	mustProbe(t, hc, 0, false, 19)
	s = stateOf(t, hc, 0)
	if !s.Healthy || s.B != 2 {
		t.Fatalf("b should restart accumulating: %+v", s)
	}
}

// 不健康态的失败清 a，打断成功累计。
func TestUnhealthyFailureResetsA(t *testing.T) {
	hc := mustNew(t, 1, 3, 1, 10, 3, 20, false, 100, 1)
	mustProbe(t, hc, 0, true, 0)  // a=1, FI
	mustProbe(t, hc, 0, false, 3) // 清 a，不健康 a=0 取 DI
	s := stateOf(t, hc, 0)
	if s.Healthy || s.A != 0 || s.ND != 23 {
		t.Fatalf("failure reset a: %+v", s)
	}
	mustProbe(t, hc, 0, true, 23)
	mustProbe(t, hc, 0, true, 26)
	mustProbe(t, hc, 0, true, 29)
	s = stateOf(t, hc, 0)
	if !s.Healthy {
		t.Fatalf("should recover after 3 fresh successes: %+v", s)
	}
}

// 转移当次按转移后的状态选间隔：向下取 DI，向上取 I（a/b 已清零）。
func TestTransitionUsesPostStateInterval(t *testing.T) {
	hc := mustNew(t, 1, 1, 2, 10, 3, 20, true, 100, 1)
	mustProbe(t, hc, 0, false, 0)
	if s := stateOf(t, hc, 0); s.ND != 3 {
		t.Fatalf("first failure nd: %+v", s)
	}
	mustProbe(t, hc, 0, false, 3)
	s := stateOf(t, hc, 0)
	if s.Healthy || s.ND != 23 || s.FU != 0 {
		t.Fatalf("post-down transition must use DI: %+v", s)
	}

	hc2 := mustNew(t, 1, 1, 1, 10, 3, 20, false, 100, 1)
	mustProbe(t, hc2, 0, true, 0)
	s = stateOf(t, hc2, 0)
	if !s.Healthy || s.ND != 10 || s.FU != 0 {
		t.Fatalf("post-up transition must use I: %+v", s)
	}
}

// 翻转窗口边界：t+Wf == now 恰好失效，差 1（t+Wf == now+1）仍有效。
func TestTransitionWindowBoundary(t *testing.T) {
	// 差 1 仍有效：Wf=201，down@0，success@200：201 > 200，g=1，Reff=2，不恢复。
	hc := mustNew(t, 1, 1, 1, 100, 3, 200, true, 201, 1)
	mustProbe(t, hc, 0, false, 0)
	mustProbe(t, hc, 0, true, 200)
	s := stateOf(t, hc, 0)
	if s.Healthy || s.A != 1 {
		t.Fatalf("one before expiry must still be active: %+v", s)
	}

	// 恰等于失效：Wf=200，200 > 200 不成立，g=0，Reff=1，立即恢复。
	hc2 := mustNew(t, 1, 1, 1, 100, 3, 200, true, 200, 1)
	mustProbe(t, hc2, 0, false, 0)
	mustProbe(t, hc2, 0, true, 200)
	s = stateOf(t, hc2, 0)
	if !s.Healthy || len(s.TR) != 2 {
		t.Fatalf("exact expiry must be inactive: %+v", s)
	}
}

// 翻转抑制：a 累计到原 R 仍不恢复；转移过期后 Reff 下降。
func TestFlapSuppressionAndDecay(t *testing.T) {
	hc := mustNew(t, 1, 2, 1, 100, 3, 20, true, 100, 1)
	mustProbe(t, hc, 0, false, 0) // down tr=[0], DI nd=20
	mustProbe(t, hc, 0, true, 20) // g=1 Reff=4, a=1
	mustProbe(t, hc, 0, true, 23) // a=2 == 原 R 仍不恢复
	s := stateOf(t, hc, 0)
	if s.Healthy || s.A != 2 {
		t.Fatalf("a==R must not recover under flap suppression: %+v", s)
	}
	// now=101：0+100 > 101 不成立，g=0，Reff=2，a=3 >= 2 恢复。
	// 若 g 仍为 1（Reff=4），a=3 不会恢复。
	mustProbe(t, hc, 0, true, 101)
	s = stateOf(t, hc, 0)
	if !s.Healthy || s.A != 0 || len(s.TR) != 2 || s.TR[1] != 101 {
		t.Fatalf("Reff must decay after transition expiry: %+v", s)
	}
}

// g 超过 4 时按 4 封顶：g=5 => Reff=1*(1+4)=5。
func TestFlapGCappedAt4(t *testing.T) {
	hc := mustNew(t, 1, 1, 1, 100, 3, 20, true, 1_000_000, 1)
	// down@0(tr1); g=1 Reff=2 -> up@23(tr2); down@123(tr3); g=3 Reff=4
	// -> up@152(tr4); down@252(tr5); 此后 g=5 封顶为 4，Reff=5。
	mustProbe(t, hc, 0, false, 0)
	mustProbe(t, hc, 0, true, 20)
	mustProbe(t, hc, 0, true, 23)
	mustProbe(t, hc, 0, false, 123)
	mustProbe(t, hc, 0, true, 143)
	mustProbe(t, hc, 0, true, 146)
	mustProbe(t, hc, 0, true, 149)
	mustProbe(t, hc, 0, true, 152)
	s := stateOf(t, hc, 0)
	if !s.Healthy || len(s.TR) != 4 {
		t.Fatalf("expected healthy with tr len 4: %+v", s)
	}
	mustProbe(t, hc, 0, false, 252) // down，tr len 5
	mustProbe(t, hc, 0, true, 272)
	mustProbe(t, hc, 0, true, 275)
	mustProbe(t, hc, 0, true, 278)
	mustProbe(t, hc, 0, true, 281) // a=4 仍不恢复
	s = stateOf(t, hc, 0)
	if s.Healthy || s.A != 4 {
		t.Fatalf("g=5 must be capped at 4 giving Reff=5: %+v", s)
	}
	mustProbe(t, hc, 0, true, 284) // a=5 恢复
	s = stateOf(t, hc, 0)
	if !s.Healthy || s.A != 0 || len(s.TR) != 6 {
		t.Fatalf("a=5 should recover under capped Reff=5: %+v", s)
	}
}

// Q=0：永不取 FI（健康 b>0 取 I，不健康 a>0 取 DI）。
func TestQZeroNeverFast(t *testing.T) {
	hc := mustNew(t, 2, 1, 3, 10, 3, 20, true, 100, 0)
	mustProbe(t, hc, 0, false, 0) // 健康 b=1，Q=0 改取 I
	s := stateOf(t, hc, 0)
	if s.ND != 10 || s.FU != 0 {
		t.Fatalf("Q=0 healthy must fall back to I: %+v", s)
	}
	hc2 := mustNew(t, 2, 2, 1, 10, 3, 20, false, 100, 0)
	mustProbe(t, hc2, 0, true, 0) // 不健康 a=1，Q=0 改取 DI
	s = stateOf(t, hc2, 0)
	if s.ND != 20 || s.FU != 0 {
		t.Fatalf("Q=0 unhealthy must fall back to DI: %+v", s)
	}
}

// x 恰为 Q-1 取 FI；x 恰为 Q 改取常规间隔。不健康态名额不足改取 DI。
func TestQuotaBoundaryAndUnhealthyFallback(t *testing.T) {
	// x==Q-1 取 FI：Q=2，仅目标 1 占用名额。
	hc2 := mustNew(t, 3, 1, 3, 10, 3, 20, true, 100, 2)
	mustProbe(t, hc2, 1, false, 0) // fu=3
	mustProbe(t, hc2, 0, false, 1) // x=1 == Q-1，取 FI，fu=4
	s0 := stateOf(t, hc2, 0)
	if s0.ND != 4 || s0.FU != 4 {
		t.Fatalf("x==Q-1 must take FI: %+v", s0)
	}
	// x==Q 改常规：t=2 时目标 1 fu=3、目标 0 fu=4 均 >2，目标 2 的 x=2==Q，取 I。
	mustProbe(t, hc2, 2, false, 2)
	s2 := stateOf(t, hc2, 2)
	if s2.ND != 12 || s2.FU != 0 {
		t.Fatalf("x==Q must fall back to I: %+v", s2)
	}

	// 不健康态因名额不足改取 DI。
	hc3 := mustNew(t, 2, 3, 1, 10, 3, 20, false, 100, 1)
	mustProbe(t, hc3, 1, true, 0) // 不健康 a=1，取 FI，fu=3
	mustProbe(t, hc3, 0, true, 1) // x=1>=Q=1，改取 DI
	s0 = stateOf(t, hc3, 0)
	if s0.Healthy || s0.A != 1 || s0.ND != 21 || s0.FU != 0 {
		t.Fatalf("unhealthy quota fallback must use DI: %+v", s0)
	}
}

// 自己上一次的 fu 不占名额：到期后再次 FI 候选时只统计其他目标。
func TestOwnFuNotCounted(t *testing.T) {
	hc := mustNew(t, 1, 1, 5, 10, 3, 20, true, 100, 1)
	mustProbe(t, hc, 0, false, 0) // b=1, FI, fu=3, nd=3
	s := stateOf(t, hc, 0)
	if s.ND != 3 || s.FU != 3 {
		t.Fatalf("setup: %+v", s)
	}
	mustProbe(t, hc, 0, false, 3) // 旧 fu=3 不大于 now=3，x=0，取 FI
	s = stateOf(t, hc, 0)
	if s.B != 2 || s.ND != 6 || s.FU != 6 {
		t.Fatalf("own expired fu must not occupy quota: %+v", s)
	}
}

// now 恰等于 nd 允许；nd-1 被拒（探测过早）。
func TestDueBoundary(t *testing.T) {
	hc := mustNew(t, 1, 1, 3, 10, 3, 20, true, 100, 1)
	mustProbe(t, hc, 0, false, 0) // nd=3
	if err := hc.Probe(0, false, 2); !errors.Is(err, ErrProbeTooEarly) {
		t.Fatalf("nd-1 should be rejected, got %v", err)
	}
	mustProbe(t, hc, 0, false, 3) // 恰等于 nd，接受
}

// 全局时钟回退：目标 nd 已到，但 now 小于已接受的最大 now，仍被拒。
func TestClockRewind(t *testing.T) {
	hc := mustNew(t, 2, 1, 3, 10, 3, 20, true, 100, 1)
	mustProbe(t, hc, 0, false, 5) // maxNow=5, nd(0)=8
	// 目标 1 nd=0 已到，但 now=4 < maxNow=5 -> 时钟回退。
	err := hc.Probe(1, false, 4)
	if !errors.Is(err, ErrClockWentBackwards) {
		t.Fatalf("clock rewind should be rejected, got %v", err)
	}
	// now == maxNow 允许。
	mustProbe(t, hc, 1, false, 5)
}

// 拒绝顺序：越界 > 时间非法 > 时钟回退 > 探测过早，只报第一个。
func TestRejectionOrder(t *testing.T) {
	hc := mustNew(t, 1, 1, 3, 10, 3, 20, true, 100, 1)
	mustProbe(t, hc, 0, false, 5)
	if err := hc.Probe(5, false, -1); !errors.Is(err, ErrTargetOutOfRange) {
		t.Fatalf("out of range first, got %v", err)
	}
	if err := hc.Probe(0, false, -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("invalid time before rewind, got %v", err)
	}
	if err := hc.Probe(0, false, maxNow+1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("now > 1e15 invalid, got %v", err)
	}
	if err := hc.Probe(0, false, 3); !errors.Is(err, ErrClockWentBackwards) {
		t.Fatalf("rewind before too-early, got %v", err)
	}
	mustProbe(t, hc, 0, false, 8) // nd=8，合法推进
	if err := hc.Probe(0, false, 9); !errors.Is(err, ErrProbeTooEarly) {
		t.Fatalf("before next nd, got %v", err)
	}
	if _, err := hc.State(-1); !errors.Is(err, ErrTargetOutOfRange) {
		t.Fatalf("State range check, got %v", err)
	}
}

// 初始不健康：计数为 0，成功走不健康恢复路径。
func TestInitialUnhealthy(t *testing.T) {
	hc := mustNew(t, 1, 2, 3, 10, 3, 20, false, 100, 1)
	s := stateOf(t, hc, 0)
	if s.Healthy || s.A != 0 || s.B != 0 || s.ND != 0 || s.FU != 0 || len(s.TR) != 0 {
		t.Fatalf("initial unhealthy snapshot: %+v", s)
	}
	if h := hc.Healthy(); len(h) != 0 {
		t.Fatalf("no healthy targets initially, got %v", h)
	}
	mustProbe(t, hc, 0, true, 0) // a=1, g=0, Reff=2, 不恢复, FI
	s = stateOf(t, hc, 0)
	if s.Healthy || s.A != 1 || s.ND != 3 {
		t.Fatalf("unhealthy success path: %+v", s)
	}
}

// 四种间隔选择各一例：I、FI（健康 b>0）、DI、FI（不健康 a>0）。
func TestFourIntervalChoices(t *testing.T) {
	hc := mustNew(t, 1, 2, 3, 10, 3, 20, true, 100, 1)
	mustProbe(t, hc, 0, true, 0) // 健康 b=0 -> I, nd=10
	if s := stateOf(t, hc, 0); s.ND != 10 {
		t.Fatalf("healthy b=0 -> I: %+v", s)
	}
	mustProbe(t, hc, 0, false, 10) // 健康 b>0 -> FI, nd=13
	if s := stateOf(t, hc, 0); s.ND != 13 || s.FU != 13 {
		t.Fatalf("healthy b>0 -> FI: %+v", s)
	}
	mustProbe(t, hc, 0, false, 13)
	mustProbe(t, hc, 0, false, 16) // b=3 -> down, a=0 -> DI, nd=36
	if s := stateOf(t, hc, 0); s.Healthy || s.ND != 36 || s.FU != 0 {
		t.Fatalf("unhealthy a=0 -> DI: %+v", s)
	}
	mustProbe(t, hc, 0, true, 36) // 不健康 a>0 -> FI, nd=39
	if s := stateOf(t, hc, 0); s.ND != 39 || s.FU != 39 {
		t.Fatalf("unhealthy a>0 -> FI: %+v", s)
	}
}

// 被拒绝的操作不改变任何状态、计数、nd、fu、tr 与最大 now。
func TestRejectedProbeChangesNothing(t *testing.T) {
	hc := mustNew(t, 2, 1, 3, 10, 3, 20, true, 100, 1)
	mustProbe(t, hc, 0, false, 5)
	before0 := stateOf(t, hc, 0)
	before1 := stateOf(t, hc, 1)

	for _, call := range []struct {
		id   int
		ok   bool
		now  int64
		want error
	}{
		{9, false, 6, ErrTargetOutOfRange},
		{0, false, -2, ErrInvalidTime},
		{1, false, 4, ErrClockWentBackwards},
		{0, false, 7, ErrProbeTooEarly}, // nd(0)=8
	} {
		err := hc.Probe(call.id, call.ok, call.now)
		if !errors.Is(err, call.want) {
			t.Fatalf("call %+v want %v got %v", call, call.want, err)
		}
		after0 := stateOf(t, hc, 0)
		after1 := stateOf(t, hc, 1)
		if !equalState(after0, before0) {
			t.Fatalf("target0 changed after rejected %+v:\nbefore=%+v\nafter =%+v", call, before0, after0)
		}
		if !equalState(after1, before1) {
			t.Fatalf("target1 changed after rejected %+v:\nbefore=%+v\nafter =%+v", call, before1, after1)
		}
	}
	// 最大 now 未变：now=5 仍可被接受（恰等于 maxNow）。
	mustProbe(t, hc, 1, false, 5)
}

func equalState(x, y TargetState) bool {
	return x.Healthy == y.Healthy && x.A == y.A && x.B == y.B &&
		x.ND == y.ND && x.FU == y.FU && equalTR(x.TR, y.TR)
}

func equalTR(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// State 返回 TR 的拷贝：外部修改不影响内部状态。
func TestStateReturnsCopy(t *testing.T) {
	hc := mustNew(t, 1, 1, 1, 10, 3, 20, true, 100, 1)
	mustProbe(t, hc, 0, false, 0)
	s := stateOf(t, hc, 0)
	s.TR[0] = 999
	s.Healthy = true
	s2 := stateOf(t, hc, 0)
	if s2.TR[0] != 0 || s2.Healthy {
		t.Fatalf("State must return a copy: %+v", s2)
	}
}

// 构造参数非法时整体拒绝。
func TestInvalidConfig(t *testing.T) {
	good := []int64{2, 2, 3, 10, 3, 20, 100, 1}
	names := []string{"N", "R", "F", "I", "FI", "DI", "Wf", "Q"}
	build := func(v []int64) error {
		_, err := NewHealthChecker(v[0], v[1], v[2], v[3], v[4], v[5], true, v[6], v[7])
		return err
	}
	if err := build(good); err != nil {
		t.Fatalf("good config rejected: %v", err)
	}
	bad := map[string][]int64{
		"N<1":    alter(good, 0, 0),
		"R<1":    alter(good, 1, 0),
		"R>1000": alter(good, 1, 1001),
		"F<1":    alter(good, 2, 0),
		"I<1":    alter(good, 3, 0),
		"I>big":  alter(good, 3, 1_000_000_001),
		"FI<1":   alter(good, 4, 0),
		"FI>big": alter(good, 4, 1_000_000_001),
		"DI<1":   alter(good, 5, 0),
		"DI>big": alter(good, 5, 1_000_000_001),
		"Wf<1":   alter(good, 6, 0),
		"Wf>big": alter(good, 6, 1_000_000_001),
		"Q<0":    alter(good, 7, -1),
		"Q>N":    alter(good, 7, 3),
	}
	for name, cfg := range bad {
		if err := build(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s should be rejected, got %v (cfg=%v)", name, err, cfg)
		}
	}
	// 边界合法值。
	for _, idx := range []int{1, 3, 4, 5, 6} {
		cfg := append([]int64(nil), good...)
		cfg[idx] = 1
		if err := build(cfg); err != nil {
			t.Fatalf("%s=1 should be valid: %v", names[idx], err)
		}
	}
	cfg := append([]int64(nil), good...)
	cfg[1] = 1000
	if err := build(cfg); err != nil {
		t.Fatalf("R=1000 should be valid: %v", err)
	}
}

func alter(base []int64, idx int, v int64) []int64 {
	out := append([]int64(nil), base...)
	out[idx] = v
	return out
}

// 并发调用：所有探测按严格递增的每目标时刻发起，全部应被接受，
// 最终不变量成立（健康 a=0，不健康 b=0），健康列表一致。
func TestConcurrentProbes(t *testing.T) {
	const n = 8
	hc := mustNew(t, n, 2, 3, 5, 2, 9, true, 1000, 3)
	var wg sync.WaitGroup
	for id := 0; id < n; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			now := int64(0)
			for step := 0; step < 200; step++ {
				ok := (id+step)%2 == 0
				for {
					if err := hc.Probe(id, ok, now); err == nil {
						break
					}
					s, _ := hc.State(id)
					if now < s.ND {
						now = s.ND
						continue
					}
					now++
				}
				s, _ := hc.State(id)
				now = s.ND
			}
		}(id)
	}
	wg.Wait()
	for id := 0; id < n; id++ {
		s := stateOf(t, hc, id)
		if s.Healthy && s.A != 0 {
			t.Fatalf("target %d healthy but a=%d", id, s.A)
		}
		if !s.Healthy && s.B != 0 {
			t.Fatalf("target %d unhealthy but b=%d", id, s.B)
		}
		for k := 1; k < len(s.TR); k++ {
			if s.TR[k] < s.TR[k-1] {
				t.Fatalf("target %d tr not nondecreasing: %v", id, s.TR)
			}
		}
	}
	// 快速名额占用数在最终观测时刻可能为 0（fu 均已过期），不做强制，仅确保不 panic。
	_ = hc.Healthy()
}
