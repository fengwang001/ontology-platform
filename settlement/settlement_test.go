package settlement_test

import (
	"errors"
	"testing"

	"ontology/settlement"
)

func requireErrKind(t *testing.T, err error, kind settlement.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind %s, got nil", kind)
	}
	var se *settlement.Error
	if !errors.As(err, &se) {
		t.Fatalf("expected *settlement.Error, got %T: %v", err, err)
	}
	if se.Kind != kind {
		t.Fatalf("expected error kind %s, got %s (%v)", kind, se.Kind, err)
	}
}

func requireNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireConserved(t *testing.T, s *settlement.Service, contractID string) settlement.Summary {
	t.Helper()
	sum, err := s.Summary(contractID)
	requireNoErr(t, err)
	if !sum.Conserved() {
		t.Fatalf("conservation violated: %+v", sum)
	}
	if sum.TotalPaid < 0 {
		t.Fatalf("negative total paid: %+v", sum)
	}
	return sum
}

func baseParams() settlement.ContractParams {
	return settlement.ContractParams{
		ID:               "c1",
		TotalAmount:      100000,
		AdvanceTotal:     0,
		AdvanceRatio:     0,
		RetentionRatio:   0,
		RetentionDays:    0,
		PenaltyDailyRate: 0,
		PenaltyCapRatio:  10000,
		Milestones: []settlement.MilestoneParams{
			{ID: "m1", Payable: 50000, PlanDay: 10},
			{ID: "m2", Payable: 50000, PlanDay: 10},
		},
	}
}

func mustCreate(t *testing.T, p settlement.ContractParams, now int64) *settlement.Service {
	t.Helper()
	s := settlement.NewService()
	requireNoErr(t, s.CreateContract(p, now))
	return s
}

// 质保金向上取整与预付款向下取整的零头。
func TestRoundingFractions(t *testing.T) {
	p := baseParams()
	p.AdvanceTotal = 100000
	p.AdvanceRatio = 5000 // 50%
	p.RetentionRatio = 1  // 0.01%
	p.Milestones = []settlement.MilestoneParams{{ID: "m1", Payable: 10001, PlanDay: 0}}
	s := mustCreate(t, p, 0)

	res, err := s.Accept("c1", "m1", true, 0)
	requireNoErr(t, err)
	// ceil(10001 * 1 / 10000) = ceil(1.0001) = 2
	if res.Retention != 2 {
		t.Fatalf("retention: want 2, got %d", res.Retention)
	}
	// floor(10001 * 5000 / 10000) = floor(5000.5) = 5000
	if res.AdvanceDeduct != 5000 {
		t.Fatalf("advance: want 5000, got %d", res.AdvanceDeduct)
	}
	if res.Paid != 10001-2-5000 {
		t.Fatalf("paid: want %d, got %d", 10001-2-5000, res.Paid)
	}
	requireConserved(t, s, "c1")
}

// 预付款抵扣恰抵清（累计恰好等于预付款总额）。
func TestAdvanceExactlyExhausted(t *testing.T) {
	p := baseParams()
	p.AdvanceTotal = 5000
	p.AdvanceRatio = 10000 // 100%
	p.Milestones = []settlement.MilestoneParams{
		{ID: "m1", Payable: 3000, PlanDay: 0},
		{ID: "m2", Payable: 3000, PlanDay: 0},
		{ID: "m3", Payable: 2000, PlanDay: 0},
	}
	s := mustCreate(t, p, 0)

	res, err := s.Accept("c1", "m1", true, 0)
	requireNoErr(t, err)
	if res.AdvanceDeduct != 3000 {
		t.Fatalf("m1 advance: want 3000, got %d", res.AdvanceDeduct)
	}

	// 剩余预付款 2000，本次按 100% 计算为 3000，超出部分不抵扣。
	res, err = s.Accept("c1", "m2", true, 0)
	requireNoErr(t, err)
	if res.AdvanceDeduct != 2000 {
		t.Fatalf("m2 advance: want 2000, got %d", res.AdvanceDeduct)
	}
	if res.Paid != 1000 {
		t.Fatalf("m2 paid: want 1000, got %d", res.Paid)
	}

	// 预付款已恰抵清，之后不再抵扣。
	res, err = s.Accept("c1", "m3", true, 0)
	requireNoErr(t, err)
	if res.AdvanceDeduct != 0 {
		t.Fatalf("m3 advance: want 0, got %d", res.AdvanceDeduct)
	}

	sum := requireConserved(t, s, "c1")
	if sum.AdvanceDeducted != 5000 {
		t.Fatalf("advance deducted: want 5000, got %d", sum.AdvanceDeducted)
	}
}

// 违约金恰触封顶：累计计提恰好等于封顶额，之后不再计提。
func TestPenaltyExactlyCapped(t *testing.T) {
	p := baseParams()
	// 封顶 = floor(100000 * 1000 / 10000) = 10000
	p.PenaltyCapRatio = 1000
	// 每日违约金 = floor(1 * 500 * 100000 / 10000) = 5000
	p.PenaltyDailyRate = 500
	p.Milestones = []settlement.MilestoneParams{
		{ID: "m1", Payable: 50000, PlanDay: 0},
		{ID: "m2", Payable: 50000, PlanDay: 0},
	}
	s := mustCreate(t, p, 0)

	// 逾期 2 天，违约金 = 2 * 5000 = 10000，恰好等于封顶。
	res, err := s.Accept("c1", "m1", true, 2)
	requireNoErr(t, err)
	if res.OverdueDays != 2 {
		t.Fatalf("overdue: want 2, got %d", res.OverdueDays)
	}
	if res.PenaltyDue != 10000 || res.PenaltyPaid != 10000 {
		t.Fatalf("penalty: want due/paid 10000/10000, got %d/%d", res.PenaltyDue, res.PenaltyPaid)
	}
	if res.Paid != 40000 {
		t.Fatalf("paid: want 40000, got %d", res.Paid)
	}

	// 第二个里程碑再逾期，累计已到封顶，本次计提为 0。
	res, err = s.Accept("c1", "m2", true, 3)
	requireNoErr(t, err)
	if res.PenaltyDue != 0 || res.PenaltyPaid != 0 {
		t.Fatalf("penalty after cap: want 0/0, got %d/%d", res.PenaltyDue, res.PenaltyPaid)
	}
	if res.Paid != 50000 {
		t.Fatalf("paid: want 50000, got %d", res.Paid)
	}

	sum := requireConserved(t, s, "c1")
	if sum.PenaltyCharged != 10000 || sum.PenaltyCap != 10000 {
		t.Fatalf("penalty charged: want 10000 (cap 10000), got %d (cap %d)", sum.PenaltyCharged, sum.PenaltyCap)
	}
}

// 违约金大于剩余时的欠额结转：欠额在下一次结算时优先扣抵。
func TestPenaltyArrearsCarryover(t *testing.T) {
	p := baseParams()
	p.PenaltyCapRatio = 10000 // 封顶 100000，不触顶
	p.PenaltyDailyRate = 500  // 每日 5000
	p.Milestones = []settlement.MilestoneParams{
		{ID: "m1", Payable: 3000, PlanDay: 0},
		{ID: "m2", Payable: 50000, PlanDay: 100},
	}
	s := mustCreate(t, p, 0)

	// m1 逾期 2 天，违约金 10000 > 应付 3000，扣到 0，欠额 7000 结转。
	res, err := s.Accept("c1", "m1", true, 2)
	requireNoErr(t, err)
	if res.PenaltyDue != 10000 {
		t.Fatalf("penalty due: want 10000, got %d", res.PenaltyDue)
	}
	if res.PenaltyPaid != 3000 || res.Paid != 0 {
		t.Fatalf("penalty paid/paid: want 3000/0, got %d/%d", res.PenaltyPaid, res.Paid)
	}
	if res.ArrearsLeft != 7000 {
		t.Fatalf("arrears left: want 7000, got %d", res.ArrearsLeft)
	}

	// m2 无逾期，但欠额优先自剩余中扣抵。
	res, err = s.Accept("c1", "m2", true, 100)
	requireNoErr(t, err)
	if res.PenaltyDue != 0 {
		t.Fatalf("penalty due: want 0, got %d", res.PenaltyDue)
	}
	if res.ArrearsPaid != 7000 {
		t.Fatalf("arrears paid: want 7000, got %d", res.ArrearsPaid)
	}
	if res.Paid != 43000 {
		t.Fatalf("paid: want 43000, got %d", res.Paid)
	}
	if res.ArrearsLeft != 0 {
		t.Fatalf("arrears left: want 0, got %d", res.ArrearsLeft)
	}

	sum := requireConserved(t, s, "c1")
	if sum.PenaltyDeducted != 10000 {
		t.Fatalf("penalty deducted: want 10000, got %d", sum.PenaltyDeducted)
	}
	if sum.ArrearsOutstanding != 0 {
		t.Fatalf("arrears: want 0, got %d", sum.ArrearsOutstanding)
	}
}

// 驳回后重验的逾期计算：驳回不改变计划日，逾期计到最终通过日。
func TestRejectThenReacceptOverdue(t *testing.T) {
	p := baseParams()
	p.PenaltyDailyRate = 100 // 每日 1000
	p.Milestones = []settlement.MilestoneParams{{ID: "m1", Payable: 50000, PlanDay: 10}}
	s := mustCreate(t, p, 0)

	// 两次驳回不改变计划日。
	res, err := s.Accept("c1", "m1", false, 12)
	requireNoErr(t, err)
	if res != nil {
		t.Fatalf("reject should return nil result, got %+v", res)
	}
	_, err = s.Accept("c1", "m1", false, 15)
	requireNoErr(t, err)

	// 第 20 天通过，逾期 = 20 - 10 = 10 天。
	res, err = s.Accept("c1", "m1", true, 20)
	requireNoErr(t, err)
	if res.PlanDay != 10 {
		t.Fatalf("plan day: want 10, got %d", res.PlanDay)
	}
	if res.OverdueDays != 10 {
		t.Fatalf("overdue: want 10, got %d", res.OverdueDays)
	}
	if res.PenaltyDue != 10000 {
		t.Fatalf("penalty due: want 10000, got %d", res.PenaltyDue)
	}
	requireConserved(t, s, "c1")
}

// 质保满日恰等（不可释放）与下一日（可释放）。
func TestRetentionExpiryBoundary(t *testing.T) {
	p := baseParams()
	p.RetentionRatio = 1000 // 10%
	p.RetentionDays = 30
	p.Milestones = []settlement.MilestoneParams{{ID: "m1", Payable: 50000, PlanDay: 0}}
	s := mustCreate(t, p, 0)

	res, err := s.Accept("c1", "m1", true, 100)
	requireNoErr(t, err)
	if res.Retention != 5000 {
		t.Fatalf("retention: want 5000, got %d", res.Retention)
	}

	// 满日 = 100 + 30 = 130，满日当天不可释放。
	_, err = s.ReleaseRetention("c1", "m1", 130)
	requireErrKind(t, err, settlement.ErrStateNotAllowed)

	// 满日的下一日起可释放。
	rel, err := s.ReleaseRetention("c1", "m1", 131)
	requireNoErr(t, err)
	if rel.Paid != 5000 || rel.DefectDeduct != 0 {
		t.Fatalf("release: want paid 5000 deduct 0, got %d/%d", rel.Paid, rel.DefectDeduct)
	}

	// 重复释放被拒绝。
	_, err = s.ReleaseRetention("c1", "m1", 132)
	requireErrKind(t, err, settlement.ErrStateNotAllowed)

	sum := requireConserved(t, s, "c1")
	_ = sum
}

// 缺陷暂停释放，关闭后的下一个操作触及时方可释放；
// 缺陷罚没从该里程碑质保金中扣抵，不影响其他里程碑。
func TestDefectPauseAndCloseRelease(t *testing.T) {
	p := baseParams()
	p.RetentionRatio = 1000 // 10%
	p.RetentionDays = 10
	p.Milestones = []settlement.MilestoneParams{
		{ID: "m1", Payable: 50000, PlanDay: 0},
		{ID: "m2", Payable: 50000, PlanDay: 0},
	}
	s := mustCreate(t, p, 0)

	_, err := s.Accept("c1", "m1", true, 0)
	requireNoErr(t, err)
	_, err = s.Accept("c1", "m2", true, 0)
	requireNoErr(t, err)

	// m1 登记缺陷，罚没 2000。
	requireNoErr(t, s.RegisterDefect("c1", "m1", "d1", 2000, 1))

	// 质保期满后，m1 有未决缺陷，暂停释放。
	_, err = s.ReleaseRetention("c1", "m1", 11)
	requireErrKind(t, err, settlement.ErrStateNotAllowed)

	// 缺陷只暂停 m1，不影响 m2。
	rel, err := s.ReleaseRetention("c1", "m2", 11)
	requireNoErr(t, err)
	if rel.Paid != 5000 || rel.DefectDeduct != 0 {
		t.Fatalf("m2 release: want 5000/0, got %d/%d", rel.Paid, rel.DefectDeduct)
	}

	// 关闭缺陷后，下一个操作触及时可释放，罚没 2000 从 m1 质保金扣抵。
	requireNoErr(t, s.CloseDefect("c1", "m1", "d1", 12))
	rel, err = s.ReleaseRetention("c1", "m1", 13)
	requireNoErr(t, err)
	if rel.Retention != 5000 || rel.DefectDeduct != 2000 || rel.Paid != 3000 {
		t.Fatalf("m1 release: want 5000/2000/3000, got %d/%d/%d", rel.Retention, rel.DefectDeduct, rel.Paid)
	}

	sum := requireConserved(t, s, "c1")
	if sum.DefectDeducted != 2000 {
		t.Fatalf("defect deducted: want 2000, got %d", sum.DefectDeducted)
	}
	if sum.RetentionBalance != 0 {
		t.Fatalf("retention balance: want 0, got %d", sum.RetentionBalance)
	}
}

// 缺陷罚没扣抵不超过该里程碑的质保金。
func TestDefectPenaltyCappedAtRetention(t *testing.T) {
	p := baseParams()
	p.RetentionRatio = 1000
	p.RetentionDays = 0
	p.Milestones = []settlement.MilestoneParams{{ID: "m1", Payable: 50000, PlanDay: 0}}
	s := mustCreate(t, p, 0)

	_, err := s.Accept("c1", "m1", true, 0)
	requireNoErr(t, err)

	// 罚没 8000 > 质保金 5000，扣抵以质保金为限。
	requireNoErr(t, s.RegisterDefect("c1", "m1", "d1", 8000, 0))
	requireNoErr(t, s.CloseDefect("c1", "m1", "d1", 0))
	rel, err := s.ReleaseRetention("c1", "m1", 1)
	requireNoErr(t, err)
	if rel.DefectDeduct != 5000 || rel.Paid != 0 {
		t.Fatalf("release: want deduct 5000 paid 0, got %d/%d", rel.DefectDeduct, rel.Paid)
	}
	requireConserved(t, s, "c1")
}

// 变更不追溯：已验收通过的里程碑不受变更影响；变更自生效日起适用。
func TestChangeOrderNotRetroactive(t *testing.T) {
	p := baseParams()
	p.PenaltyDailyRate = 100 // 每日 1000
	p.Milestones = []settlement.MilestoneParams{
		{ID: "m1", Payable: 30000, PlanDay: 10},
		{ID: "m2", Payable: 30000, PlanDay: 10},
	}
	s := mustCreate(t, p, 0)

	// m1 在第 10 天通过，无逾期。
	res, err := s.Accept("c1", "m1", true, 10)
	requireNoErr(t, err)
	if res.OverdueDays != 0 || res.Payable != 30000 {
		t.Fatalf("m1: want payable 30000 overdue 0, got %d/%d", res.Payable, res.OverdueDays)
	}

	// 变更 m1（已通过）被拒绝：状态不允许。
	err = s.ChangeOrder("c1", 11, []settlement.Adjustment{{MilestoneID: "m1", Payable: 10000, PlanDay: 5}}, 11)
	requireErrKind(t, err, settlement.ErrStateNotAllowed)

	// 变更 m2：应付改为 20000，计划日改为 20，生效日 15。
	requireNoErr(t, s.ChangeOrder("c1", 15, []settlement.Adjustment{{MilestoneID: "m2", Payable: 20000, PlanDay: 20}}, 12))

	// m2 在第 18 天通过，适用变更：应付 20000，计划日 20，无逾期。
	res, err = s.Accept("c1", "m2", true, 18)
	requireNoErr(t, err)
	if res.Payable != 20000 || res.PlanDay != 20 || res.OverdueDays != 0 {
		t.Fatalf("m2: want 20000/20/0, got %d/%d/%d", res.Payable, res.PlanDay, res.OverdueDays)
	}

	// m1 已结算结果不被追溯改变。
	sum := requireConserved(t, s, "c1")
	if sum.SettledPayableSum != 50000 {
		t.Fatalf("settled sum: want 50000, got %d", sum.SettledPayableSum)
	}
}

// 变更在生效日之前验收的里程碑不适用变更。
func TestChangeOrderEffectiveDay(t *testing.T) {
	p := baseParams()
	p.Milestones = []settlement.MilestoneParams{{ID: "m1", Payable: 30000, PlanDay: 10}}
	s := mustCreate(t, p, 0)

	// 生效日 100 的变更。
	requireNoErr(t, s.ChangeOrder("c1", 100, []settlement.Adjustment{{MilestoneID: "m1", Payable: 10000, PlanDay: 50}}, 10))

	// 第 20 天验收，早于生效日，仍用旧值。
	res, err := s.Accept("c1", "m1", true, 20)
	requireNoErr(t, err)
	if res.Payable != 30000 || res.PlanDay != 10 {
		t.Fatalf("want old values 30000/10, got %d/%d", res.Payable, res.PlanDay)
	}
	requireConserved(t, s, "c1")
}

// 变更后各里程碑应付之和超过合同总金额则拒绝，且不改变任何状态。
func TestChangeOrderExceedsTotal(t *testing.T) {
	p := baseParams()
	p.Milestones = []settlement.MilestoneParams{
		{ID: "m1", Payable: 60000, PlanDay: 10},
		{ID: "m2", Payable: 30000, PlanDay: 10},
	}
	s := mustCreate(t, p, 0)

	// 60000 + 50000 = 110000 > 100000，拒绝。
	err := s.ChangeOrder("c1", 5, []settlement.Adjustment{{MilestoneID: "m2", Payable: 50000, PlanDay: 10}}, 1)
	requireErrKind(t, err, settlement.ErrChangeExceedsTotal)

	// 被拒绝的变更不生效：m2 仍按旧值结算。
	res, err := s.Accept("c1", "m2", true, 10)
	requireNoErr(t, err)
	if res.Payable != 30000 {
		t.Fatalf("payable: want 30000, got %d", res.Payable)
	}
	requireConserved(t, s, "c1")
}

// 终止合同后的处理：未结算里程碑不再产生应付款，
// 已扣留质保金仍按原规则释放，欠额不再扣抵。
func TestTerminateContract(t *testing.T) {
	p := baseParams()
	p.RetentionRatio = 1000
	p.RetentionDays = 5
	p.PenaltyDailyRate = 500 // 每日 5000
	p.Milestones = []settlement.MilestoneParams{
		{ID: "m1", Payable: 3000, PlanDay: 0},
		{ID: "m2", Payable: 50000, PlanDay: 0},
	}
	s := mustCreate(t, p, 0)

	// m1 逾期 2 天通过：违约金 10000 > 剩余，产生欠额；质保金 300。
	res, err := s.Accept("c1", "m1", true, 2)
	requireNoErr(t, err)
	if res.ArrearsLeft == 0 {
		t.Fatalf("expected arrears after m1, got 0")
	}

	requireNoErr(t, s.Terminate("c1", 3))

	// 未结算的 m2 不再验收。
	_, err = s.Accept("c1", "m2", true, 4)
	requireErrKind(t, err, settlement.ErrStateNotAllowed)

	// 欠额不再扣抵（保持不变）。
	sum, err := s.Summary("c1")
	requireNoErr(t, err)
	arrearsBefore := sum.ArrearsOutstanding
	if arrearsBefore == 0 {
		t.Fatalf("expected outstanding arrears, got 0")
	}

	// 已扣留的质保金仍按原规则释放（满日 2+5=7，第 8 天可释放）。
	rel, err := s.ReleaseRetention("c1", "m1", 8)
	requireNoErr(t, err)
	if rel.Paid != 300 {
		t.Fatalf("release paid: want 300, got %d", rel.Paid)
	}

	// 欠额保持不變，守恒仍成立。
	sum = requireConserved(t, s, "c1")
	if sum.ArrearsOutstanding != arrearsBefore {
		t.Fatalf("arrears changed after terminate: want %d, got %d", arrearsBefore, sum.ArrearsOutstanding)
	}
	if !sum.Terminated {
		t.Fatalf("expected terminated")
	}

	// 重复终止被拒绝。
	requireErrKind(t, s.Terminate("c1", 9), settlement.ErrStateNotAllowed)
}

// 违约金大于剩余时的欠额结转：欠额在下一次结算时优先于本次违约金扣抵。
func TestArrearsCarryoverPriority(t *testing.T) {
	p := baseParams()
	p.PenaltyDailyRate = 100 // 每日 floor(1*100*100000/10000)=1000
	p.Milestones = []settlement.MilestoneParams{
		{ID: "m1", Payable: 2000, PlanDay: 0},
		{ID: "m2", Payable: 10000, PlanDay: 100},
	}
	s := mustCreate(t, p, 0)

	// m1 逾期 5 天：违约金 5000 > 应付 2000，实付 0，欠额 3000。
	res, err := s.Accept("c1", "m1", true, 5)
	requireNoErr(t, err)
	if res.PenaltyDue != 5000 || res.PenaltyPaid != 2000 || res.Paid != 0 || res.ArrearsLeft != 3000 {
		t.Fatalf("m1: want due/paid/paid/arrears 5000/2000/0/3000, got %d/%d/%d/%d",
			res.PenaltyDue, res.PenaltyPaid, res.Paid, res.ArrearsLeft)
	}

	// m2 也逾期 5 天（计划日 100，通过日 105）：本次违约金 5000，
	// 但欠额 3000 优先扣抵，剩余 7000 中再扣本次 5000，实付 2000。
	res, err = s.Accept("c1", "m2", true, 105)
	requireNoErr(t, err)
	if res.ArrearsPaid != 3000 {
		t.Fatalf("arrears paid: want 3000, got %d", res.ArrearsPaid)
	}
	if res.PenaltyDue != 5000 || res.PenaltyPaid != 5000 {
		t.Fatalf("penalty: want 5000/5000, got %d/%d", res.PenaltyDue, res.PenaltyPaid)
	}
	if res.Paid != 2000 {
		t.Fatalf("paid: want 2000, got %d", res.Paid)
	}
	if res.ArrearsLeft != 0 {
		t.Fatalf("arrears left: want 0, got %d", res.ArrearsLeft)
	}

	sum := requireConserved(t, s, "c1")
	if sum.PenaltyDeducted != 10000 || sum.ArrearsOutstanding != 0 {
		t.Fatalf("penalty deducted: want 10000/0, got %d/%d", sum.PenaltyDeducted, sum.ArrearsOutstanding)
	}
}

// 欠额同样受累计封顶约束：计提达到封顶后不再产生欠额。
func TestArrearsBoundedByCap(t *testing.T) {
	p := baseParams()
	p.PenaltyDailyRate = 100 // 每日 1000
	p.PenaltyCapRatio = 50   // 封顶 = floor(100000*50/10000) = 500
	p.Milestones = []settlement.MilestoneParams{
		{ID: "m1", Payable: 100, PlanDay: 0},
		{ID: "m2", Payable: 100, PlanDay: 0},
	}
	s := mustCreate(t, p, 0)

	// m1 逾期 10 天：计提 10000，封顶后 500；扣抵 100，欠额 400。
	res, err := s.Accept("c1", "m1", true, 10)
	requireNoErr(t, err)
	if res.PenaltyDue != 500 || res.PenaltyPaid != 100 || res.ArrearsLeft != 400 {
		t.Fatalf("m1: want 500/100/400, got %d/%d/%d", res.PenaltyDue, res.PenaltyPaid, res.ArrearsLeft)
	}

	// m2 再逾期：累计计提已到封顶，本次计提为 0，只扣抵欠额。
	res, err = s.Accept("c1", "m2", true, 10)
	requireNoErr(t, err)
	if res.PenaltyDue != 0 || res.ArrearsPaid != 100 || res.ArrearsLeft != 300 {
		t.Fatalf("m2: want 0/100/300, got %d/%d/%d", res.PenaltyDue, res.ArrearsPaid, res.ArrearsLeft)
	}

	sum := requireConserved(t, s, "c1")
	if sum.PenaltyCharged != 500 || sum.PenaltyCap != 500 {
		t.Fatalf("penalty charged: want 500 (cap 500), got %d (cap %d)", sum.PenaltyCharged, sum.PenaltyCap)
	}
}

// 时钟回退：now 小于上一次被接受操作的 now 被拒绝，且不改变时钟。
func TestClockRollback(t *testing.T) {
	s := mustCreate(t, baseParams(), 10)

	// now=9 < 10，回退。
	_, err := s.Accept("c1", "m1", true, 9)
	requireErrKind(t, err, settlement.ErrClockRollback)

	// 被拒绝的操作不改变时钟：now=10 仍被接受。
	_, err = s.Accept("c1", "m1", true, 10)
	requireNoErr(t, err)

	// now 相等允许。
	_, err = s.Accept("c1", "m2", true, 10)
	requireNoErr(t, err)

	// 时钟跨操作类型生效。
	requireErrKind(t, s.Terminate("c1", 9), settlement.ErrClockRollback)
	requireNoErr(t, s.Terminate("c1", 11))
	requireConserved(t, s, "c1")
}

// 重复结算：已通过的里程碑再次验收（无论通过或驳回）都被拒绝。
func TestDuplicateSettlement(t *testing.T) {
	s := mustCreate(t, baseParams(), 0)

	_, err := s.Accept("c1", "m1", true, 0)
	requireNoErr(t, err)

	_, err = s.Accept("c1", "m1", true, 1)
	requireErrKind(t, err, settlement.ErrDuplicateSettlement)
	_, err = s.Accept("c1", "m1", false, 1)
	requireErrKind(t, err, settlement.ErrDuplicateSettlement)

	// 重复结算被拒绝不改变任何状态。
	sum := requireConserved(t, s, "c1")
	if sum.SettledPayableSum != 50000 {
		t.Fatalf("settled sum: want 50000, got %d", sum.SettledPayableSum)
	}
}

// 拒绝优先级：同时违反多条规则时只报第一个类别。
func TestRejectionPriority(t *testing.T) {
	p := baseParams()
	p.Milestones = []settlement.MilestoneParams{
		{ID: "m1", Payable: 90000, PlanDay: 0},
		{ID: "m2", Payable: 10000, PlanDay: 0},
	}
	s := mustCreate(t, p, 10)

	// 参数非法 优先于 时钟回退：空 ID + now 回退。
	_, err := s.Accept("c1", "", true, 5)
	requireErrKind(t, err, settlement.ErrInvalidParam)

	// 时钟回退 优先于 不存在：不存在的合同 + now 回退。
	_, err = s.Accept("nope", "m1", true, 5)
	requireErrKind(t, err, settlement.ErrClockRollback)

	// 不存在 优先于 状态不允许：不存在的里程碑（合同已终止时同样先报不存在）。
	_, err = s.Accept("c1", "nope", true, 10)
	requireErrKind(t, err, settlement.ErrNotFound)

	// 状态不允许 优先于 重复结算：先通过 m1，再终止，再验收 m1。
	_, err = s.Accept("c1", "m1", true, 10)
	requireNoErr(t, err)
	requireNoErr(t, s.Terminate("c1", 11))
	_, err = s.Accept("c1", "m1", true, 11)
	requireErrKind(t, err, settlement.ErrStateNotAllowed)

	// 重复结算 优先于 变更超出合同总额（不同操作，各自独立验证）：
	// 重复结算。
	s2 := mustCreate(t, p, 0)
	_, err = s2.Accept("c1", "m1", true, 0)
	requireNoErr(t, err)
	_, err = s2.Accept("c1", "m1", true, 0)
	requireErrKind(t, err, settlement.ErrDuplicateSettlement)
	// 变更超出合同总额。
	err = s2.ChangeOrder("c1", 1, []settlement.Adjustment{{MilestoneID: "m2", Payable: 20000, PlanDay: 0}}, 0)
	requireErrKind(t, err, settlement.ErrChangeExceedsTotal)

	// 状态不允许 优先于 变更超出合同总额：对已通过的里程碑做超额变更。
	err = s2.ChangeOrder("c1", 1, []settlement.Adjustment{{MilestoneID: "m1", Payable: 99999, PlanDay: 0}}, 0)
	requireErrKind(t, err, settlement.ErrStateNotAllowed)

	// 被拒绝的操作均未改变状态：m2 仍按旧值结算。
	res, err := s2.Accept("c1", "m2", true, 0)
	requireNoErr(t, err)
	if res.Payable != 10000 {
		t.Fatalf("payable: want 10000, got %d", res.Payable)
	}
	requireConserved(t, s2, "c1")
}

// 质保金与预付款抵扣合计不超过应付，实付不得为负。
func TestPaidNeverNegative(t *testing.T) {
	p := baseParams()
	p.AdvanceTotal = 100000
	p.AdvanceRatio = 10000   // 100%
	p.RetentionRatio = 10000 // 100%
	p.Milestones = []settlement.MilestoneParams{{ID: "m1", Payable: 7777, PlanDay: 0}}
	s := mustCreate(t, p, 0)

	res, err := s.Accept("c1", "m1", true, 0)
	requireNoErr(t, err)
	if res.Retention != 7777 {
		t.Fatalf("retention: want 7777, got %d", res.Retention)
	}
	// 质保金已扣留全额，预付款抵扣被钳制为 0。
	if res.AdvanceDeduct != 0 {
		t.Fatalf("advance: want 0, got %d", res.AdvanceDeduct)
	}
	if res.Paid != 0 {
		t.Fatalf("paid: want 0, got %d", res.Paid)
	}
	requireConserved(t, s, "c1")
}

// 创建合同的参数校验。
func TestCreateContractValidation(t *testing.T) {
	s := settlement.NewService()

	bad := baseParams()
	bad.ID = ""
	requireErrKind(t, s.CreateContract(bad, 0), settlement.ErrInvalidParam)

	bad = baseParams()
	bad.TotalAmount = 0
	requireErrKind(t, s.CreateContract(bad, 0), settlement.ErrInvalidParam)

	bad = baseParams()
	bad.RetentionRatio = 10001
	requireErrKind(t, s.CreateContract(bad, 0), settlement.ErrInvalidParam)

	bad = baseParams()
	bad.Milestones = []settlement.MilestoneParams{
		{ID: "m1", Payable: 60000, PlanDay: 0},
		{ID: "m1", Payable: 1000, PlanDay: 0},
	}
	requireErrKind(t, s.CreateContract(bad, 0), settlement.ErrInvalidParam)

	bad = baseParams()
	bad.Milestones = []settlement.MilestoneParams{{ID: "m1", Payable: 100001, PlanDay: 0}}
	requireErrKind(t, s.CreateContract(bad, 0), settlement.ErrInvalidParam)

	// 合法创建后，重复 ID 报状态不允许。
	requireNoErr(t, s.CreateContract(baseParams(), 0))
	requireErrKind(t, s.CreateContract(baseParams(), 0), settlement.ErrStateNotAllowed)
}

// 多次变更生效日交错：验收时适用生效日不晚于验收日的最新版本。
func TestChangeOrderInterleavedEffectiveDays(t *testing.T) {
	p := baseParams()
	p.Milestones = []settlement.MilestoneParams{{ID: "m1", Payable: 10000, PlanDay: 0}}
	s := mustCreate(t, p, 0)

	// 变更1：生效日 200，应付 30000。
	requireNoErr(t, s.ChangeOrder("c1", 200, []settlement.Adjustment{{MilestoneID: "m1", Payable: 30000, PlanDay: 0}}, 10))
	// 变更2：生效日 100（更早），应付 20000。
	requireNoErr(t, s.ChangeOrder("c1", 100, []settlement.Adjustment{{MilestoneID: "m1", Payable: 20000, PlanDay: 0}}, 20))

	// 第 150 天验收：仅变更2生效。
	res, err := s.Accept("c1", "m1", true, 150)
	requireNoErr(t, err)
	if res.Payable != 20000 {
		t.Fatalf("payable at day 150: want 20000, got %d", res.Payable)
	}
	requireConserved(t, s, "c1")

	// 另起合同，第 250 天验收：变更1（生效日 200）最新生效。
	s2 := mustCreate(t, p, 0)
	requireNoErr(t, s2.ChangeOrder("c1", 200, []settlement.Adjustment{{MilestoneID: "m1", Payable: 30000, PlanDay: 0}}, 10))
	requireNoErr(t, s2.ChangeOrder("c1", 100, []settlement.Adjustment{{MilestoneID: "m1", Payable: 20000, PlanDay: 0}}, 20))
	res, err = s2.Accept("c1", "m1", true, 250)
	requireNoErr(t, err)
	if res.Payable != 30000 {
		t.Fatalf("payable at day 250: want 30000, got %d", res.Payable)
	}
	requireConserved(t, s2, "c1")
}
