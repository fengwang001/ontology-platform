package settlement

import (
	"errors"
	"testing"
)

// wantErr 断言错误类别。
func wantErr(t *testing.T, err error, code ErrorCode) {
	t.Helper()
	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("期望 %s，得到非服务错误: %v", code, err)
	}
	if se.Code != code {
		t.Fatalf("期望 %s，得到 %s: %v", code, se.Code, err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，得到错误: %v", err)
	}
}

// checkConserved 校验合同守恒不变式。
func checkConserved(t *testing.T, s *Service, contractID string) Summary {
	t.Helper()
	sum, err := s.Summary(contractID)
	mustOK(t, err)
	if !sum.Conserved() {
		t.Fatalf("守恒被破坏: %+v", sum)
	}
	return sum
}

// baseSpec 通用合同：总额 1_000_000，预付款 100_000（10% 抵扣），
// 质保金 5%，质保期 30 天，违约金 0.1%/日（1000/日），封顶 5%（50_000）。
func baseSpec() ContractSpec {
	return ContractSpec{
		TotalAmount:      1_000_000,
		AdvanceTotal:     100_000,
		AdvanceRatio:     1000,
		RetentionRatio:   500,
		WarrantyDays:     30,
		PenaltyDailyRate: 10,
		PenaltyCapRatio:  500,
		Milestones: []MilestoneSpec{
			{ID: "m1", Amount: 200_000, PlanDay: 10},
			{ID: "m2", Amount: 300_000, PlanDay: 20},
			{ID: "m3", Amount: 400_000, PlanDay: 30},
		},
	}
}

func newContractService(t *testing.T, spec ContractSpec) *Service {
	t.Helper()
	s := NewService()
	mustOK(t, s.CreateContract("c1", spec, 0))
	return s
}

// TestRoundingFractions 质保金向上取整与预付款向下取整的零头。
func TestRoundingFractions(t *testing.T) {
	spec := ContractSpec{
		TotalAmount:    1_000_000,
		AdvanceTotal:   1_000_000,
		AdvanceRatio:   333, // 3.33%
		RetentionRatio: 333, // 3.33%
		WarrantyDays:   10,
		Milestones: []MilestoneSpec{
			{ID: "m1", Amount: 999, PlanDay: 0},
			{ID: "m2", Amount: 10_000, PlanDay: 0},
		},
	}
	s := newContractService(t, spec)

	// 999*333/10000 = 33.2667：质保金向上取整 34，预付款向下取整 33。
	if _, err := s.AcceptMilestone("c1", "m1", true, 0); err != nil {
		t.Fatal(err)
	}
	r, err := s.SettleMilestone("c1", "m1", 0)
	mustOK(t, err)
	if r.Retention != 34 || r.AdvanceDeducted != 33 {
		t.Fatalf("零头取整错误: retention=%d(期望34) advance=%d(期望33)", r.Retention, r.AdvanceDeducted)
	}
	if r.Payment != 999-34-33 {
		t.Fatalf("实付错误: %d", r.Payment)
	}

	// 10_000*333/10000 = 333 整除：向上向下均为 333。
	if _, err := s.AcceptMilestone("c1", "m2", true, 0); err != nil {
		t.Fatal(err)
	}
	r2, err := s.SettleMilestone("c1", "m2", 0)
	mustOK(t, err)
	if r2.Retention != 333 || r2.AdvanceDeducted != 333 {
		t.Fatalf("整除情形错误: retention=%d advance=%d", r2.Retention, r2.AdvanceDeducted)
	}
	checkConserved(t, s, "c1")
}

// TestAdvanceExactlyExhausted 预付款抵扣恰抵清：计算抵扣恰等于剩余预付款。
func TestAdvanceExactlyExhausted(t *testing.T) {
	spec := baseSpec()
	spec.AdvanceTotal = 20_000 // m1 计算抵扣 200_000*10% = 20_000，恰抵清
	s := newContractService(t, spec)

	if _, err := s.AcceptMilestone("c1", "m1", true, 10); err != nil {
		t.Fatal(err)
	}
	r, err := s.SettleMilestone("c1", "m1", 10)
	mustOK(t, err)
	if r.AdvanceDeducted != 20_000 {
		t.Fatalf("恰抵清失败: %d", r.AdvanceDeducted)
	}
	sum := checkConserved(t, s, "c1")
	if sum.AdvanceDeductedTotal != 20_000 {
		t.Fatalf("累计预付款抵扣应为 20000: %d", sum.AdvanceDeductedTotal)
	}

	// 预付款已抵清，后续里程碑不再抵扣。
	if _, err := s.AcceptMilestone("c1", "m2", true, 20); err != nil {
		t.Fatal(err)
	}
	r2, err := s.SettleMilestone("c1", "m2", 20)
	mustOK(t, err)
	if r2.AdvanceDeducted != 0 {
		t.Fatalf("预付款抵清后不应再抵扣: %d", r2.AdvanceDeducted)
	}
	checkConserved(t, s, "c1")
}

// TestAdvanceCapped 预付款抵扣累计不得超过预付款总额，超出部分不抵扣。
func TestAdvanceCapped(t *testing.T) {
	spec := baseSpec()
	spec.AdvanceTotal = 25_000 // m1 抵扣 20_000 后仅剩 5_000
	s := newContractService(t, spec)

	if _, err := s.AcceptMilestone("c1", "m1", true, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleMilestone("c1", "m1", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptMilestone("c1", "m2", true, 20); err != nil {
		t.Fatal(err)
	}
	r, err := s.SettleMilestone("c1", "m2", 20)
	mustOK(t, err)
	// m2 计算抵扣 300_000*10% = 30_000，但只剩 5_000 可抵。
	if r.AdvanceDeducted != 5_000 {
		t.Fatalf("预付款抵扣应被总额限制为 5000: %d", r.AdvanceDeducted)
	}
	sum := checkConserved(t, s, "c1")
	if sum.AdvanceDeductedTotal != 25_000 {
		t.Fatalf("累计预付款抵扣应等于预付款总额: %d", sum.AdvanceDeductedTotal)
	}
}

// TestPenaltyExactlyAtCap 违约金恰触封顶。
func TestPenaltyExactlyAtCap(t *testing.T) {
	spec := baseSpec()
	spec.RetentionRatio = 0
	spec.AdvanceRatio = 0
	spec.AdvanceTotal = 0
	// 日费率 0.1% × 总额 1_000_000 = 1000/日；封顶 5% = 50_000。
	s := newContractService(t, spec)

	// m1 逾期 50 天：违约金 = 50*1000 = 50_000，恰触封顶。
	if _, err := s.AcceptMilestone("c1", "m1", true, 60); err != nil { // 计划日 10
		t.Fatal(err)
	}
	r, err := s.SettleMilestone("c1", "m1", 60)
	mustOK(t, err)
	if r.OverdueDays != 50 {
		t.Fatalf("逾期天数错误: %d", r.OverdueDays)
	}
	if r.PenaltyAssessed != 50_000 || r.PenaltyDeducted != 50_000 {
		t.Fatalf("违约金应恰触封顶 50000: assessed=%d deducted=%d", r.PenaltyAssessed, r.PenaltyDeducted)
	}

	// m2 再逾期：计提空间为 0，不再产生违约金。
	if _, err := s.AcceptMilestone("c1", "m2", true, 100); err != nil { // 计划日 20，逾期 80
		t.Fatal(err)
	}
	r2, err := s.SettleMilestone("c1", "m2", 100)
	mustOK(t, err)
	if r2.PenaltyAssessed != 0 {
		t.Fatalf("封顶后不应再计提违约金: %d", r2.PenaltyAssessed)
	}
	sum := checkConserved(t, s, "c1")
	if sum.PenaltyDeductedTotal != 50_000 {
		t.Fatalf("累计违约金扣抵应为 50000: %d", sum.PenaltyDeductedTotal)
	}
}

// TestPenaltyDebtCarryover 违约金大于剩余时的欠额结转，且下次结算优先扣抵。
func TestPenaltyDebtCarryover(t *testing.T) {
	spec := ContractSpec{
		TotalAmount:      1_000_000,
		AdvanceTotal:     1_000_000,
		AdvanceRatio:     4000, // 40%
		RetentionRatio:   5000, // 50%
		WarrantyDays:     30,
		PenaltyDailyRate: 100, // 1%/日 = 10_000/日
		PenaltyCapRatio:  10_000,
		Milestones: []MilestoneSpec{
			{ID: "m1", Amount: 100_000, PlanDay: 0},
			{ID: "m2", Amount: 100_000, PlanDay: 10},
		},
	}
	s := newContractService(t, spec)

	// m1：质保 50_000 + 预付 40_000，剩余 10_000；逾期 5 天违约金 50_000，
	// 只能扣抵 10_000，欠额 40_000 结转。
	if _, err := s.AcceptMilestone("c1", "m1", true, 5); err != nil {
		t.Fatal(err)
	}
	r, err := s.SettleMilestone("c1", "m1", 5)
	mustOK(t, err)
	if r.PenaltyAssessed != 50_000 || r.PenaltyDeducted != 10_000 ||
		r.DebtCarriedIn != 0 || r.DebtCarriedOut != 40_000 || r.Payment != 0 {
		t.Fatalf("欠额结转错误: %+v", r)
	}

	// m2：不逾期（无新计提），但欠额 40_000 优先扣抵，剩余 10_000 全部用于扣抵。
	if _, err := s.AcceptMilestone("c1", "m2", true, 10); err != nil {
		t.Fatal(err)
	}
	r2, err := s.SettleMilestone("c1", "m2", 10)
	mustOK(t, err)
	if r2.PenaltyAssessed != 0 || r2.DebtCarriedIn != 40_000 ||
		r2.PenaltyDeducted != 10_000 || r2.DebtCarriedOut != 30_000 || r2.Payment != 0 {
		t.Fatalf("欠额优先扣抵错误: %+v", r2)
	}
	sum := checkConserved(t, s, "c1")
	if sum.OutstandingPenaltyDebt != 30_000 {
		t.Fatalf("欠额应为 30000: %d", sum.OutstandingPenaltyDebt)
	}
	if sum.PenaltyDeductedTotal != 20_000 {
		t.Fatalf("累计违约金扣抵应为 20000: %d", sum.PenaltyDeductedTotal)
	}
}

// TestRejectThenReacceptOverdue 驳回后重验：计划日不变，逾期计到最终通过日。
func TestRejectThenReacceptOverdue(t *testing.T) {
	s := newContractService(t, baseSpec())

	// m1 计划日 10；第 12、14 日两次驳回，第 15 日通过。
	r1, err := s.AcceptMilestone("c1", "m1", false, 12)
	mustOK(t, err)
	if r1.Passed {
		t.Fatal("应为驳回")
	}
	if _, err := s.AcceptMilestone("c1", "m1", false, 14); err != nil {
		t.Fatal(err)
	}
	r2, err := s.AcceptMilestone("c1", "m1", true, 15)
	mustOK(t, err)
	if r2.OverdueDays != 5 {
		t.Fatalf("驳回不改变计划日，逾期应计到通过日: %d(期望5)", r2.OverdueDays)
	}
	if r2.PlanDay != 10 {
		t.Fatalf("计划日不应被驳回改变: %d", r2.PlanDay)
	}
	v, err := s.InspectMilestone("c1", "m1")
	mustOK(t, err)
	if v.PlanDay != 10 || v.OverdueDays != 5 {
		t.Fatalf("里程碑状态错误: %+v", v)
	}
	// 违约金按逾期 5 天计提：5*1000 = 5_000。
	sr, err := s.SettleMilestone("c1", "m1", 15)
	mustOK(t, err)
	if sr.PenaltyAssessed != 5_000 {
		t.Fatalf("违约金计提错误: %d(期望5000)", sr.PenaltyAssessed)
	}
	checkConserved(t, s, "c1")
}

// settleM1 辅助：m1 在第 5 日验收通过并结算（质保期满日为 35）。
func settleM1(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.AcceptMilestone("c1", "m1", true, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleMilestone("c1", "m1", 5); err != nil {
		t.Fatal(err)
	}
}

// TestWarrantyBoundary 质保满日恰等（不释放）与下一日（可释放）。
func TestWarrantyBoundary(t *testing.T) {
	s := newContractService(t, baseSpec()) // 质保期 30 天
	settleM1(t, s)                         // 通过日 5，满日 35

	// 满日当天（35）尚未释放；被拒绝的操作不推进时钟。
	_, err := s.ReleaseRetention("c1", "m1", 35)
	wantErr(t, err, ErrCodeInvalidState)

	// 满日次日（36）可释放。m1 质保金 = 200_000*5% = 10_000。
	r, err := s.ReleaseRetention("c1", "m1", 36)
	mustOK(t, err)
	if r.Withheld != 10_000 || r.Forfeit != 0 || r.PaidOut != 10_000 {
		t.Fatalf("释放结果错误: %+v", r)
	}
	sum := checkConserved(t, s, "c1")
	// m1 计划日 10、通过日 5，无逾期：实付 = 200_000-10_000(质保)-20_000(预付) = 170_000，
	// 释放再支付 10_000。
	if sum.RetentionHeld != 0 || sum.PaidTotal != 170_000+10_000 {
		t.Fatalf("汇总错误: %+v", sum)
	}
}

// TestDefectPauseAndRelease 缺陷暂停释放、关闭后释放、罚没扣抵、按里程碑隔离。
func TestDefectPauseAndRelease(t *testing.T) {
	s := newContractService(t, baseSpec())
	settleM1(t, s) // m1 质保金 10_000，满日 35

	// m2 也结算（通过日 20，计划日 20，满日 50）。
	if _, err := s.AcceptMilestone("c1", "m2", true, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleMilestone("c1", "m2", 20); err != nil {
		t.Fatal(err)
	}

	// m1 登记两个缺陷：罚没 8_000 与 5_000（合计 13_000 > 质保金 10_000）。
	mustOK(t, s.RegisterDefect("c1", "m1", "d1", 8_000, 25))
	mustOK(t, s.RegisterDefect("c1", "m1", "d2", 5_000, 26))

	// 未决缺陷暂停 m1 释放（即使已过满日）。
	_, err := s.ReleaseRetention("c1", "m1", 36)
	wantErr(t, err, ErrCodeInvalidState)

	// 缺陷只暂停本里程碑：m2 满日次日可正常释放。
	r2, err := s.ReleaseRetention("c1", "m2", 51)
	mustOK(t, err)
	if r2.PaidOut != 15_000 { // 300_000*5%
		t.Fatalf("m2 释放不应受 m1 缺陷影响: %+v", r2)
	}

	// 关闭一个缺陷后仍有未决缺陷，仍暂停。
	mustOK(t, s.CloseDefect("c1", "m1", "d1", 52))
	_, err = s.ReleaseRetention("c1", "m1", 53)
	wantErr(t, err, ErrCodeInvalidState)

	// 全部关闭后的下一个操作触及时可释放；罚没扣抵不超过质保金。
	mustOK(t, s.CloseDefect("c1", "m1", "d2", 54))
	r, err := s.ReleaseRetention("c1", "m1", 55)
	mustOK(t, err)
	if r.Withheld != 10_000 || r.Forfeit != 10_000 || r.PaidOut != 0 {
		t.Fatalf("罚没扣抵应封顶于质保金: %+v", r)
	}
	sum := checkConserved(t, s, "c1")
	if sum.DefectForfeitTotal != 10_000 {
		t.Fatalf("缺陷扣抵累计错误: %d", sum.DefectForfeitTotal)
	}
}

// TestAmendNotRetroactive 变更不追溯：已验收/已结算不变，未验收的按生效日适用。
func TestAmendNotRetroactive(t *testing.T) {
	s := newContractService(t, baseSpec())
	settleM1(t, s) // m1 已结算（金额 200_000）

	// 已验收通过的里程碑不可变更。
	err := s.AmendContract("c1", 6, []Change{{MilestoneID: "m1", Amount: 1, PlanDay: 1}}, 6)
	wantErr(t, err, ErrCodeInvalidState)

	// 变更 m2：金额 300_000→350_000，计划日 20→25，生效日 15。
	mustOK(t, s.AmendContract("c1", 15, []Change{{MilestoneID: "m2", Amount: 350_000, PlanDay: 25}}, 10))
	// 变更 m3：生效日 40（未来），第 35 日验收时仍适用旧条款。
	mustOK(t, s.AmendContract("c1", 40, []Change{{MilestoneID: "m3", Amount: 390_000, PlanDay: 45}}, 11))

	// m2 第 20 日验收（>= 生效日 15）：适用新条款，计划日 25，无逾期。
	ar, err := s.AcceptMilestone("c1", "m2", true, 20)
	mustOK(t, err)
	if ar.Amount != 350_000 || ar.PlanDay != 25 || ar.OverdueDays != 0 {
		t.Fatalf("变更未按生效日适用: %+v", ar)
	}
	sr, err := s.SettleMilestone("c1", "m2", 20)
	mustOK(t, err)
	if sr.Amount != 350_000 {
		t.Fatalf("结算应按变更后金额: %d", sr.Amount)
	}

	// m3 第 35 日验收（< 生效日 40）：适用旧条款（400_000，计划日 30，逾期 5）。
	ar3, err := s.AcceptMilestone("c1", "m3", true, 35)
	mustOK(t, err)
	if ar3.Amount != 400_000 || ar3.PlanDay != 30 || ar3.OverdueDays != 5 {
		t.Fatalf("未来生效的变更不应提前适用: %+v", ar3)
	}

	// m1 已结算金额不受变更影响（其结算结果在变更前已确定）。
	v, err := s.InspectMilestone("c1", "m1")
	mustOK(t, err)
	if v.Amount != 200_000 || !v.Settled {
		t.Fatalf("已结算里程碑被追溯: %+v", v)
	}
	checkConserved(t, s, "c1")
}

// TestAmendExceedsTotal 变更后各里程碑应付之和不得超过合同总金额。
func TestAmendExceedsTotal(t *testing.T) {
	s := newContractService(t, baseSpec())
	// m3 改为 500_001：200_000+300_000+500_001 = 1_000_001 > 1_000_000。
	err := s.AmendContract("c1", 5, []Change{{MilestoneID: "m3", Amount: 500_001, PlanDay: 30}}, 5)
	wantErr(t, err, ErrCodeChangeExceedsTotal)
	// 改为 500_000：恰等于总额，允许。
	mustOK(t, s.AmendContract("c1", 5, []Change{{MilestoneID: "m3", Amount: 500_000, PlanDay: 30}}, 5))
	checkConserved(t, s, "c1")
}

// TestTermination 终止合同后的处理。
func TestTermination(t *testing.T) {
	s := newContractService(t, baseSpec())
	settleM1(t, s) // m1 已结算，质保金 10_000，满日 35

	mustOK(t, s.TerminateContract("c1", 10))

	// 未验收里程碑不再验收、不再结算。
	_, err := s.AcceptMilestone("c1", "m2", true, 11)
	wantErr(t, err, ErrCodeInvalidState)
	_, err = s.SettleMilestone("c1", "m2", 11)
	wantErr(t, err, ErrCodeInvalidState)
	// 不可再变更。
	err = s.AmendContract("c1", 12, []Change{{MilestoneID: "m2", Amount: 1, PlanDay: 1}}, 12)
	wantErr(t, err, ErrCodeInvalidState)
	// 重复终止。
	wantErr(t, s.TerminateContract("c1", 13), ErrCodeInvalidState)

	// 已扣留质保金仍按原规则释放。
	r, err := s.ReleaseRetention("c1", "m1", 36)
	mustOK(t, err)
	if r.PaidOut != 10_000 {
		t.Fatalf("终止后质保金应照常释放: %+v", r)
	}
	sum := checkConserved(t, s, "c1")
	if sum.SettledAmountTotal != 200_000 {
		t.Fatalf("已结算的保持不变: %+v", sum)
	}
}

// TestRejectionPriority 错误类别可区分，且按优先级只报第一个；
// 被拒绝的操作不改变任何状态、欠额与时钟。
func TestRejectionPriority(t *testing.T) {
	s := newContractService(t, baseSpec())
	settleM1(t, s) // 时钟推进到 5；m1 已结算

	cases := []struct {
		name string
		op   func() error
		want ErrorCode
	}{
		// 参数非法 优先于 时钟回退（now=-1 且回退）。
		{"参数非法>时钟回退", func() error {
			_, err := s.SettleMilestone("", "m2", -1)
			return err
		}, ErrCodeInvalidArgument},
		// 时钟回退 优先于 不存在（now=4 < 5 且合同不存在）。
		{"时钟回退>不存在", func() error {
			_, err := s.SettleMilestone("ghost", "m2", 4)
			return err
		}, ErrCodeClockRollback},
		// 不存在 优先于 状态不允许（里程碑不存在；m1 已结算本属重复结算）。
		{"不存在>状态/重复", func() error {
			_, err := s.SettleMilestone("c1", "ghost", 6)
			return err
		}, ErrCodeNotFound},
		// 状态不允许 优先于 重复结算（m2 未验收；m1 已结算）。
		{"状态不允许>重复结算", func() error {
			_, err := s.SettleMilestone("c1", "m2", 6)
			return err
		}, ErrCodeInvalidState},
		// 重复结算（m1 已结算，无其他违规）。
		{"重复结算", func() error {
			_, err := s.SettleMilestone("c1", "m1", 6)
			return err
		}, ErrCodeAlreadySettled},
		// 变更超出合同总额（无其他违规）。
		{"变更超出总额", func() error {
			return s.AmendContract("c1", 6, []Change{{MilestoneID: "m3", Amount: 700_001, PlanDay: 30}}, 6)
		}, ErrCodeChangeExceedsTotal},
		// 状态不允许 优先于 变更超出总额（合同终止后变更，即使超额）。
		{"状态不允许>变更超额", func() error {
			mustOK(t, s.TerminateContract("c1", 7))
			return s.AmendContract("c1", 8, []Change{{MilestoneID: "m3", Amount: 700_001, PlanDay: 30}}, 8)
		}, ErrCodeInvalidState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantErr(t, tc.op(), tc.want)
		})
	}

	// 被拒绝的操作不推进时钟：最后一次被接受操作 now=7（终止），
	// 之后全部被拒；now=7 仍应被接受（等于上次，非回退）。
	err := s.RegisterDefect("c1", "m1", "d1", 1, 7)
	mustOK(t, err)
}

// TestRejectedOpKeepsState 被拒绝的操作不改变状态、欠额与时钟。
func TestRejectedOpKeepsState(t *testing.T) {
	s := newContractService(t, baseSpec())
	settleM1(t, s)
	before := checkConserved(t, s, "c1")

	// 一系列被拒绝的操作。
	_, _ = s.SettleMilestone("c1", "m1", 3)       // 时钟回退
	_, _ = s.SettleMilestone("c1", "m1", 6)       // 重复结算
	_, _ = s.ReleaseRetention("c1", "m1", 6)      // 质保期未满
	_ = s.TerminateContract("ghost", 6)           // 合同不存在
	_ = s.CloseDefect("c1", "m1", "ghost", 6)     // 缺陷不存在
	_ = s.RegisterDefect("c1", "m1", "d1", -1, 6) // 参数非法

	after := checkConserved(t, s, "c1")
	if before != after {
		t.Fatalf("被拒绝的操作改变了状态:\n前=%+v\n后=%+v", before, after)
	}
	// 时钟未被拒绝操作推进：now=5（等于上次被接受操作）仍被接受。
	mustOK(t, s.RegisterDefect("c1", "m1", "d1", 1, 5))
}

// TestReplayDeterminism 相同操作序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	rec := &Recorder{}
	s := NewService()
	s.SetRecorder(rec)

	spec := baseSpec()
	mustOK(t, s.CreateContract("c1", spec, 0))
	if _, err := s.AcceptMilestone("c1", "m1", false, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptMilestone("c1", "m1", true, 8); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleMilestone("c1", "m1", 9); err != nil {
		t.Fatal(err)
	}
	mustOK(t, s.RegisterDefect("c1", "m1", "d1", 500, 10))
	mustOK(t, s.AmendContract("c1", 12, []Change{{MilestoneID: "m2", Amount: 310_000, PlanDay: 22}}, 11))
	if _, err := s.AcceptMilestone("c1", "m2", true, 25); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettleMilestone("c1", "m2", 26); err != nil {
		t.Fatal(err)
	}
	mustOK(t, s.CloseDefect("c1", "m1", "d1", 40))
	if _, err := s.ReleaseRetention("c1", "m1", 41); err != nil {
		t.Fatal(err)
	}
	mustOK(t, s.TerminateContract("c1", 42))

	replayed, err := Replay(rec.Snapshot())
	mustOK(t, err)
	got, err := replayed.Summary("c1")
	mustOK(t, err)
	want := checkConserved(t, s, "c1")
	if got != want {
		t.Fatalf("重放结果不一致:\n原=%+v\n放=%+v", want, got)
	}
}

// TestSummaryConstantWork 汇总查询开销不随历史结算与缺陷记录总数增长：
// Summary 只做常数次字段读取，验证其零分配（无迭代、无拷贝）。
func TestSummaryConstantWork(t *testing.T) {
	spec := baseSpec()
	// 扩充里程碑以制造较长历史。
	for i := 0; i < 50; i++ {
		spec.Milestones = append(spec.Milestones,
			MilestoneSpec{ID: "x" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Amount: 0, PlanDay: 0})
	}
	s := newContractService(t, spec)
	settleM1(t, s)
	// 制造大量缺陷记录与结算历史。
	for i := 0; i < 2000; i++ {
		mustOK(t, s.RegisterDefect("c1", "m1", "d"+string(rune(i)), 1, 6))
	}
	allocs := testing.AllocsPerRun(100, func() {
		if _, err := s.Summary("c1"); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Summary 应为零分配（常数开销），实际 %v 次分配", allocs)
	}
}
