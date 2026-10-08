package subro_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/subro"
	"ontology/subro/alloc"
	"ontology/subro/ledger"
)

func mustRegister(t *testing.T, s *subro.Service, now int64, in subro.CaseInput) {
	t.Helper()
	if err := s.RegisterCase(now, in); err != nil {
		t.Fatalf("RegisterCase(%q) 失败: %v", in.CaseID, err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("操作应被接受但被拒绝: %v", err)
	}
}

func logResult(t *testing.T, op string, res subro.OpResult, snap subro.Snapshot) {
	t.Helper()
	t.Logf("op=%s -> ent={insured:%d insurer:%d third:%d} disbursed={insured:%d insurer:%d third:%d} adj=%v",
		op, res.Entitlements.Insured, res.Entitlements.Insurer, res.Entitlements.ThirdParty,
		res.Disbursed.Insured, res.Disbursed.Insurer, res.Disbursed.ThirdParty, res.Adjustments)
	t.Logf("  判定依据: netTotal=%d cap=%d distributable=%d uncompensated=%d paid=%d waived=%v",
		snap.NetTotal, snap.Cap, snap.Distributable, snap.Uncompensated, snap.InsurerPaid, snap.Waived)
}

func wantEnt(t *testing.T, got alloc.Entitlements, insured, insurer, third int64) {
	t.Helper()
	want := alloc.Entitlements{Insured: insured, Insurer: insurer, ThirdParty: third}
	if got != want {
		t.Fatalf("应得=%+v, 期望 %+v", got, want)
	}
}

func wantAdj(t *testing.T, got, want []ledger.Adjustment) {
	t.Helper()
	strip := func(in []ledger.Adjustment) []ledger.Adjustment {
		out := make([]ledger.Adjustment, len(in))
		for i, a := range in {
			a.Seq = 0
			out[i] = a
		}
		return out
	}
	if !reflect.DeepEqual(strip(got), strip(want)) {
		t.Fatalf("调整记录=%v, 期望 %v", got, want)
	}
}

func adj(p ledger.Party, d ledger.Direction, amount, now int64, cause string) ledger.Adjustment {
	return ledger.Adjustment{Party: p, Direction: d, Amount: amount, Now: now, Cause: cause}
}

// 回收恰在时效截止日（含当日）被接受，晚一日报已过时效。
func TestRecoverExactlyAtDeadline(t *testing.T) {
	s := subro.NewService()
	mustRegister(t, s, 0, subro.CaseInput{CaseID: "c1", TotalLoss: 10000, InsurerPaid: 6000, Deadline: 100, RatioBP: 5000})

	res, err := s.Recover(100, "c1", 2000, 100)
	mustOK(t, err)
	snap, _ := s.Snapshot("c1")
	logResult(t, "recover(now=100,gross=2000,fee=100)", res, snap)
	wantEnt(t, res.Entitlements, 1900, 0, 0)
	wantAdj(t, res.Adjustments, []ledger.Adjustment{
		adj(ledger.Insured, ledger.Pay, 1900, 100, "recover"),
	})

	if _, err := s.Recover(101, "c1", 100, 0); !errors.Is(err, subro.ErrExpired) {
		t.Fatalf("截止日后回收应报已过时效, 得到 %v", err)
	}
	if _, err := s.Waive(101, "c1"); !errors.Is(err, subro.ErrExpired) {
		t.Fatalf("截止日后放弃应报已过时效, 得到 %v", err)
	}
}

// 净回收总额恰等于可追偿上限时不产生超额。
func TestNetEqualsCapExactly(t *testing.T) {
	s := subro.NewService()
	mustRegister(t, s, 0, subro.CaseInput{CaseID: "c1", TotalLoss: 10000, InsurerPaid: 6000, Deadline: 100, RatioBP: 5000})

	res, err := s.Recover(1, "c1", 5000, 0)
	mustOK(t, err)
	snap, _ := s.Snapshot("c1")
	logResult(t, "recover(gross=5000,fee=0) cap=5000", res, snap)
	if snap.NetTotal != snap.Cap {
		t.Fatalf("净回收总额 %d 应恰等于上限 %d", snap.NetTotal, snap.Cap)
	}
	wantEnt(t, res.Entitlements, 4000, 1000, 0)
	wantAdj(t, res.Adjustments, []ledger.Adjustment{
		adj(ledger.Insured, ledger.Pay, 4000, 1, "recover"),
		adj(ledger.Insurer, ledger.Pay, 1000, 1, "recover"),
	})
}

// 费用恰等于毛额时净回收为零，不产生任何发放与调整。
func TestFeeEqualsGross(t *testing.T) {
	s := subro.NewService()
	mustRegister(t, s, 0, subro.CaseInput{CaseID: "c1", TotalLoss: 10000, InsurerPaid: 6000, Deadline: 100, RatioBP: 10000})

	res, err := s.Recover(1, "c1", 3000, 3000)
	mustOK(t, err)
	snap, _ := s.Snapshot("c1")
	logResult(t, "recover(gross=3000,fee=3000)", res, snap)
	if snap.NetTotal != 0 || snap.GrossTotal != 3000 || snap.FeeTotal != 3000 {
		t.Fatalf("净回收应为 0, 快照 %+v", snap)
	}
	wantEnt(t, res.Entitlements, 0, 0, 0)
	if len(res.Adjustments) != 0 {
		t.Fatalf("净回收为零不应产生调整记录, 得到 %v", res.Adjustments)
	}
}

// 责任比例调低使净回收超过新上限时，按分配顺序的逆序追回：
// 先追回保险人份额，再追回被保险人份额，超额退还第三方。
func TestRatioDecreaseReverseClawback(t *testing.T) {
	s := subro.NewService()
	mustRegister(t, s, 0, subro.CaseInput{CaseID: "c1", TotalLoss: 10000, InsurerPaid: 6000, Deadline: 100, RatioBP: 10000})

	res, err := s.Recover(1, "c1", 9000, 0)
	mustOK(t, err)
	wantEnt(t, res.Entitlements, 4000, 5000, 0)

	// 上限降到 4000：保险人 5000 -> 0 被全额追回，被保险人 4000 不动。
	res, err = s.AdjustRatio(2, "c1", 4000)
	mustOK(t, err)
	snap, _ := s.Snapshot("c1")
	logResult(t, "adjust_ratio(4000)", res, snap)
	wantEnt(t, res.Entitlements, 4000, 0, 5000)
	wantAdj(t, res.Adjustments, []ledger.Adjustment{
		adj(ledger.Insurer, ledger.Clawback, 5000, 2, "adjust_ratio"),
		adj(ledger.ThirdParty, ledger.Pay, 5000, 2, "adjust_ratio"),
	})

	// 上限再降到 2000：保险人已无份额可追，轮到被保险人 4000 -> 2000。
	res, err = s.AdjustRatio(3, "c1", 2000)
	mustOK(t, err)
	snap, _ = s.Snapshot("c1")
	logResult(t, "adjust_ratio(2000)", res, snap)
	wantEnt(t, res.Entitlements, 2000, 0, 7000)
	wantAdj(t, res.Adjustments, []ledger.Adjustment{
		adj(ledger.Insured, ledger.Clawback, 2000, 3, "adjust_ratio"),
		adj(ledger.ThirdParty, ledger.Pay, 2000, 3, "adjust_ratio"),
	})
}

// 放弃追偿权后被保险人份额视为零，保险人顺延获得但不超过已赔付额，
// 其余为超额退还第三方；放弃立即触发结清。
func TestWaiveShiftAndCap(t *testing.T) {
	s := subro.NewService()
	mustRegister(t, s, 0, subro.CaseInput{CaseID: "c1", TotalLoss: 10000, InsurerPaid: 6000, Deadline: 100, RatioBP: 10000})

	res, err := s.Recover(1, "c1", 9000, 0)
	mustOK(t, err)
	wantEnt(t, res.Entitlements, 4000, 5000, 0)

	// 声明放弃：被保险人 4000 -> 0，保险人顺延至已赔付额 6000 封顶，
	// 其余 3000 属超额退还第三方。
	res, err = s.Waive(2, "c1")
	mustOK(t, err)
	snap, _ := s.Snapshot("c1")
	logResult(t, "waive", res, snap)
	wantEnt(t, res.Entitlements, 0, 6000, 3000)
	wantAdj(t, res.Adjustments, []ledger.Adjustment{
		adj(ledger.Insured, ledger.Clawback, 4000, 2, "waive"),
		adj(ledger.Insurer, ledger.Pay, 1000, 2, "waive"),
		adj(ledger.ThirdParty, ledger.Pay, 3000, 2, "waive"),
	})

	// 放弃只能做一次。
	if _, err := s.Waive(3, "c1"); !errors.Is(err, subro.ErrAlreadyWaived) {
		t.Fatalf("重复放弃应报已放弃, 得到 %v", err)
	}

	// 放弃后新回收仍按顺延与封顶分配。
	res, err = s.Recover(4, "c1", 5000, 0)
	mustOK(t, err)
	snap, _ = s.Snapshot("c1")
	logResult(t, "recover(gross=5000) after waive", res, snap)
	wantEnt(t, res.Entitlements, 0, 6000, 8000)
}

// 放弃须在时效截止日之前（含当日）。
func TestWaiveDeadlineBoundary(t *testing.T) {
	s := subro.NewService()
	mustRegister(t, s, 0, subro.CaseInput{CaseID: "c1", TotalLoss: 1000, InsurerPaid: 500, Deadline: 10, RatioBP: 10000})
	if _, err := s.Waive(10, "c1"); err != nil {
		t.Fatalf("截止日当日放弃应被接受: %v", err)
	}

	s2 := subro.NewService()
	mustRegister(t, s2, 0, subro.CaseInput{CaseID: "c2", TotalLoss: 1000, InsurerPaid: 500, Deadline: 10, RatioBP: 10000})
	if _, err := s2.Waive(11, "c2"); !errors.Is(err, subro.ErrExpired) {
		t.Fatalf("截止日后放弃应报已过时效, 得到 %v", err)
	}
}

// 补充赔付使保险人已赔付额增加、被保险人未获赔额等额减少，
// 应得随之变化并立即结清；两者之和始终等于总损失额。
func TestSupplementChangesEntitlements(t *testing.T) {
	s := subro.NewService()
	mustRegister(t, s, 0, subro.CaseInput{CaseID: "c1", TotalLoss: 10000, InsurerPaid: 4000, Deadline: 100, RatioBP: 10000})

	res, err := s.Recover(1, "c1", 9000, 0)
	mustOK(t, err)
	wantEnt(t, res.Entitlements, 6000, 3000, 0)

	// 补充赔付 2500：paid 4000 -> 6500，未获赔额 6000 -> 3500。
	res, err = s.Supplement(2, "c1", 2500)
	mustOK(t, err)
	snap, _ := s.Snapshot("c1")
	logResult(t, "supplement(2500)", res, snap)
	if snap.InsurerPaid+snap.Uncompensated != snap.TotalLoss {
		t.Fatalf("已赔付额与未获赔额之和应恒等于总损失额: %+v", snap)
	}
	wantEnt(t, res.Entitlements, 3500, 5500, 0)
	wantAdj(t, res.Adjustments, []ledger.Adjustment{
		adj(ledger.Insured, ledger.Clawback, 2500, 2, "supplement"),
		adj(ledger.Insurer, ledger.Pay, 2500, 2, "supplement"),
	})

	// 补充赔付不得使已赔付额超过总损失额。
	if _, err := s.Supplement(3, "c1", 4000); !errors.Is(err, subro.ErrExceedsTotalLoss) {
		t.Fatalf("补充赔付超出总损失额应报错, 得到 %v", err)
	}
	// 恰好补足到总损失额是合法的。
	res, err = s.Supplement(4, "c1", 3500)
	mustOK(t, err)
	wantEnt(t, res.Entitlements, 0, 9000, 0)
}

// 调整记录必须是应得与已发放的最小差：不得先全额追回再重发。
func TestMinimalAdjustment(t *testing.T) {
	s := subro.NewService()
	mustRegister(t, s, 0, subro.CaseInput{CaseID: "c1", TotalLoss: 10000, InsurerPaid: 6000, Deadline: 100, RatioBP: 10000})

	res, err := s.Recover(1, "c1", 5000, 0)
	mustOK(t, err)
	wantEnt(t, res.Entitlements, 4000, 1000, 0)

	// 上限从 10000 调到 8000：净回收 5000 仍不超上限，应得不变，零记录。
	res, err = s.AdjustRatio(2, "c1", 8000)
	mustOK(t, err)
	if len(res.Adjustments) != 0 {
		t.Fatalf("应得不变不应产生调整记录, 得到 %v", res.Adjustments)
	}

	// 上限降到 3000：被保险人多发了 1000，只追回差额 1000 而非全额 4000。
	res, err = s.AdjustRatio(3, "c1", 3000)
	mustOK(t, err)
	snap, _ := s.Snapshot("c1")
	logResult(t, "adjust_ratio(3000)", res, snap)
	wantAdj(t, res.Adjustments, []ledger.Adjustment{
		adj(ledger.Insured, ledger.Clawback, 1000, 3, "adjust_ratio"),
		adj(ledger.Insurer, ledger.Clawback, 1000, 3, "adjust_ratio"),
		adj(ledger.ThirdParty, ledger.Pay, 2000, 3, "adjust_ratio"),
	})
	for _, a := range res.Adjustments {
		if a.Party == ledger.Insured && a.Amount > 1000 {
			t.Fatalf("追回必须是最小差，发现全额追回再重发: %v", a)
		}
	}
}

// 相同操作序列重放得到完全相同的应得、已发放与调整记录。
func TestReplayDeterminism(t *testing.T) {
	run := func() []subro.Snapshot {
		s := subro.NewService()
		mustRegister(t, s, 0, subro.CaseInput{CaseID: "a", TotalLoss: 10000, InsurerPaid: 6000, Deadline: 50, RatioBP: 8000})
		mustRegister(t, s, 0, subro.CaseInput{CaseID: "b", TotalLoss: 5000, InsurerPaid: 1000, Deadline: 60, RatioBP: 3000})
		ops := []subro.Op{
			{Kind: subro.OpRecover, Now: 1, CaseID: "a", Gross: 3000, Fee: 500},
			{Kind: subro.OpRecover, Now: 2, CaseID: "b", Gross: 2000, Fee: 0},
			{Kind: subro.OpAdjustRatio, Now: 3, CaseID: "a", RatioBP: 4000},
			{Kind: subro.OpSupplement, Now: 4, CaseID: "a", Amount: 1500},
			{Kind: subro.OpWaive, Now: 5, CaseID: "b"},
			{Kind: subro.OpRecover, Now: 6, CaseID: "a", Gross: 7000, Fee: 1000},
		}
		for _, op := range ops {
			if _, err := s.Apply(op); err != nil {
				t.Fatalf("op %+v 应被接受: %v", op, err)
			}
		}
		var snaps []subro.Snapshot
		for _, id := range []string{"a", "b"} {
			snap, _ := s.Snapshot(id)
			snaps = append(snaps, snap)
		}
		return snaps
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放结果不一致:\n第一次 %+v\n第二次 %+v", first, second)
	}
}

// 被拒绝的操作不得改变任何状态、已发放记录与时钟。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	s := subro.NewService()
	mustRegister(t, s, 0, subro.CaseInput{CaseID: "c1", TotalLoss: 10000, InsurerPaid: 6000, Deadline: 100, RatioBP: 10000})
	if _, err := s.Recover(10, "c1", 4000, 500); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Waive(11, "c1"); err != nil {
		t.Fatal(err)
	}

	before, _ := s.Snapshot("c1")
	lastNow, _ := s.LastNow()

	rejected := []struct {
		name string
		op   subro.Op
		want error
	}{
		{"费用大于毛额", subro.Op{Kind: subro.OpRecover, Now: 12, CaseID: "c1", Gross: 100, Fee: 200}, subro.ErrInvalidParam},
		{"责任比例越界", subro.Op{Kind: subro.OpAdjustRatio, Now: 12, CaseID: "c1", RatioBP: 10001}, subro.ErrInvalidParam},
		{"时钟回退", subro.Op{Kind: subro.OpRecover, Now: 5, CaseID: "c1", Gross: 100, Fee: 0}, subro.ErrClockRollback},
		{"案件不存在", subro.Op{Kind: subro.OpRecover, Now: 12, CaseID: "ghost", Gross: 100, Fee: 0}, subro.ErrCaseNotFound},
		{"已过时效", subro.Op{Kind: subro.OpRecover, Now: 101, CaseID: "c1", Gross: 100, Fee: 0}, subro.ErrExpired},
		{"超出总损失额", subro.Op{Kind: subro.OpSupplement, Now: 12, CaseID: "c1", Amount: 99999}, subro.ErrExceedsTotalLoss},
		{"重复放弃", subro.Op{Kind: subro.OpWaive, Now: 12, CaseID: "c1"}, subro.ErrAlreadyWaived},
	}
	for _, tc := range rejected {
		if _, err := s.Apply(tc.op); !errors.Is(err, tc.want) {
			t.Fatalf("%s: 期望错误 %v, 得到 %v", tc.name, tc.want, err)
		}
		after, _ := s.Snapshot("c1")
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: 被拒绝操作改变了状态:\n前 %+v\n后 %+v", tc.name, before, after)
		}
		now, _ := s.LastNow()
		if now != lastNow {
			t.Fatalf("%s: 被拒绝操作推进了时钟 %d -> %d", tc.name, lastNow, now)
		}
		t.Logf("rejected op=%s kind=%v -> err=%v, 状态与时钟无变化", tc.name, tc.op.Kind, tc.want)
	}
}

// 错误按优先级只报第一个：
// 参数非法 > 时钟回退 > 案件不存在 > 已过时效 > 超出总损失额 > 已放弃。
func TestErrorPriority(t *testing.T) {
	s := subro.NewService()
	mustRegister(t, s, 0, subro.CaseInput{CaseID: "c1", TotalLoss: 10000, InsurerPaid: 6000, Deadline: 10, RatioBP: 10000})
	if _, err := s.Waive(5, "c1"); err != nil {
		t.Fatal(err)
	}
	// 当前时钟 now=5；c1 截止日 10，且已放弃。

	cases := []struct {
		name string
		op   subro.Op
		want error
	}{
		// 同时违反全部规则：报参数非法。
		{"参数非法优先于一切", subro.Op{Kind: subro.OpRecover, Now: 1, CaseID: "ghost", Gross: 100, Fee: 200}, subro.ErrInvalidParam},
		{"时钟回退优先于案件不存在", subro.Op{Kind: subro.OpRecover, Now: 1, CaseID: "ghost", Gross: 100, Fee: 0}, subro.ErrClockRollback},
		{"案件不存在优先于已过时效", subro.Op{Kind: subro.OpRecover, Now: 99, CaseID: "ghost", Gross: 100, Fee: 0}, subro.ErrCaseNotFound},
		{"已过时效优先于已放弃", subro.Op{Kind: subro.OpWaive, Now: 11, CaseID: "c1"}, subro.ErrExpired},
		{"已放弃在最后", subro.Op{Kind: subro.OpWaive, Now: 6, CaseID: "c1"}, subro.ErrAlreadyWaived},
		{"补充赔付无时效检查,超出总损失额生效", subro.Op{Kind: subro.OpSupplement, Now: 99, CaseID: "c1", Amount: 99999}, subro.ErrExceedsTotalLoss},
	}
	for _, tc := range cases {
		_, err := s.Apply(tc.op)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: 期望 %v, 得到 %v", tc.name, tc.want, err)
		}
		t.Logf("priority op=%s -> %v (符合优先级)", tc.name, tc.want)
	}
}
