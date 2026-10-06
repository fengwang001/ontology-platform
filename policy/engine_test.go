package policy

import (
	"reflect"
	"sync"
	"testing"
)

func baseInput(id string) PolicyInput {
	return PolicyInput{
		ID:              id,
		EffectiveDate:   0,
		AnnualPremium:   1000,
		SumInsured:      10000,
		MinSumInsured:   1000,
		HesitationDays:  15,
		IssueFee:        30,
		CashValueRatios: []int64{10, 20},
		PaymentTerm:     20,
		Beneficiary:     "init",
	}
}

func mustCode(t *testing.T, err error, code ErrCode) {
	t.Helper()
	e, ok := errOf(err)
	if !ok || e.Code != code {
		t.Fatalf("want err code %d, got %v", code, err)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func mustSnapshot(t *testing.T, e *Engine, id string) Snapshot {
	t.Helper()
	snap, err := e.Snapshot(id)
	mustOK(t, err)
	return snap
}

// 保单年度右端取等落入下一档。
func TestPolicyYearRightOpen(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy(baseInput("p")))
	mustOK(t, e.PayPremium("p", 0, 1000))
	cv, err := e.CashValue("p", 364, 0)
	mustOK(t, err)
	if cv != 100 {
		t.Fatalf("day 364 want 100, got %d", cv)
	}
	cv, err = e.CashValue("p", 365, 0)
	mustOK(t, err)
	if cv != 200 {
		t.Fatalf("day 365 want 200 (next tier), got %d", cv)
	}
}

// 比例表未给出的年度沿用最后一档。
func TestRatioTableFallback(t *testing.T) {
	e := NewEngine()
	in := baseInput("p")
	in.CashValueRatios = []int64{30}
	mustOK(t, e.RegisterPolicy(in))
	mustOK(t, e.PayPremium("p", 0, 1000))
	mustOK(t, e.PayPremium("p", 1, 1000)) // 预缴下一年度
	if _, err := e.Advance("p", 730); err != nil {
		t.Fatal(err)
	}
	mustOK(t, e.PayPremium("p", 2, 1000))
	cv, err := e.CashValue("p", 3*365+10, 0)
	mustOK(t, err)
	if cv != 900 {
		t.Fatalf("year 3 want 3000*30%%=900, got %d", cv)
	}
}

// 现金价值减借款本息后不足为零。
func TestCashValueLoanFlooredAtZero(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy(baseInput("p")))
	mustOK(t, e.PayPremium("p", 0, 1000))
	cv, err := e.CashValue("p", 100, 150)
	mustOK(t, err)
	if cv != 0 {
		t.Fatalf("want 0, got %d", cv)
	}
	cv, err = e.CashValue("p", 100, 99)
	mustOK(t, err)
	if cv != 1 {
		t.Fatalf("want 1, got %d", cv)
	}
}

// 犹豫期末日退保与次日退保。
func TestHesitationBoundary(t *testing.T) {
	newPolicy := func() *Engine {
		e := NewEngine()
		in := baseInput("p")
		in.EffectiveDate = 100
		in.CashValueRatios = []int64{100}
		mustOK(t, e.RegisterPolicy(in))
		mustOK(t, e.PayPremium("p", 0, 1000))
		return e
	}
	// 犹豫期末日（100+15-1=114）：退还全部实缴减工本费。
	e1 := newPolicy()
	if _, err := e1.Advance("p", 114); err != nil {
		t.Fatal(err)
	}
	refund, err := e1.Surrender("p", 0)
	mustOK(t, err)
	if refund != 970 {
		t.Fatalf("hesitation last day want 970, got %d", refund)
	}
	// 犹豫期次日（115）：退现金价值。
	e2 := newPolicy()
	if _, err := e2.Advance("p", 115); err != nil {
		t.Fatal(err)
	}
	refund, err = e2.Surrender("p", 0)
	mustOK(t, err)
	if refund != 1000 {
		t.Fatalf("after hesitation want cash value 1000, got %d", refund)
	}
	// 退保后为终态。
	mustCode(t, func() error { _, err := e2.Advance("p", 200); return err }(), ErrTerminated)
}

// 生效日恰等于当前时刻的预约可登记，并在下一次推进时生效。
func TestScheduleEffDayEqualsNow(t *testing.T) {
	e := NewEngine()
	in := baseInput("p")
	in.EffectiveDate = 50
	mustOK(t, e.RegisterPolicy(in))
	err := e.ScheduleEndorsement("p", EndorsementInput{
		ID: "e1", Type: EndorsementBeneficiary, ApplyDay: 50, EffDay: 50, Beneficiary: "alice",
	})
	mustOK(t, err)
	events, err := e.Advance("p", 50)
	mustOK(t, err)
	if len(events) != 1 || events[0].Kind != EventEffective || events[0].EndorsementID != "e1" {
		t.Fatalf("want e1 effective, got %+v", events)
	}
	if snap := mustSnapshot(t, e, "p"); snap.Beneficiary != "alice" {
		t.Fatalf("want alice, got %s", snap.Beneficiary)
	}
}

// 同日多条预约按申请次序生效。
func TestSameDayOrderByApplication(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy(baseInput("p")))
	mustOK(t, e.ScheduleEndorsement("p", EndorsementInput{
		ID: "first", Type: EndorsementBeneficiary, ApplyDay: 0, EffDay: 10, Beneficiary: "alice",
	}))
	mustOK(t, e.ScheduleEndorsement("p", EndorsementInput{
		ID: "second", Type: EndorsementBeneficiary, ApplyDay: 0, EffDay: 10, Beneficiary: "bob",
	}))
	events, err := e.Advance("p", 10)
	mustOK(t, err)
	if len(events) != 2 || events[0].EndorsementID != "first" || events[1].EndorsementID != "second" {
		t.Fatalf("want application order, got %+v", events)
	}
	if snap := mustSnapshot(t, e, "p"); snap.Beneficiary != "bob" {
		t.Fatalf("later application wins, want bob, got %s", snap.Beneficiary)
	}
}

// 保额增加：本年度已缴时转待补缴，补缴按剩余天数占比向上取整。
func TestSumInsuredIncreaseTopUpCeil(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy(baseInput("p")))
	mustOK(t, e.PayPremium("p", 0, 1000))
	mustOK(t, e.ScheduleEndorsement("p", EndorsementInput{
		ID: "up", Type: EndorsementSumInsured, ApplyDay: 0, EffDay: 300, NewSumInsured: 15000,
	}))
	events, err := e.Advance("p", 300)
	mustOK(t, err)
	// 新保费 ceil(1000*15000/10000)=1500，差额 500，剩余 65 天，
	// 补缴 ceil(500*65/365)=90。
	if len(events) != 1 || events[0].Kind != EventPendingTopUp || events[0].TopUp != 90 {
		t.Fatalf("want pending top-up 90, got %+v", events)
	}
	snap := mustSnapshot(t, e, "p")
	if snap.SumInsured != 10000 || snap.Premium != 1000 || snap.TotalPaid != 1000 {
		t.Fatalf("pending top-up must not change ledger: %+v", snap)
	}
	if snap.Endorsements["up"].State != StatePendingTopUp {
		t.Fatalf("want pending state, got %+v", snap.Endorsements["up"])
	}
	paid, err := e.PayTopUp("p", "up")
	mustOK(t, err)
	if paid != 90 {
		t.Fatalf("want top-up 90, got %d", paid)
	}
	snap = mustSnapshot(t, e, "p")
	if snap.SumInsured != 15000 || snap.Premium != 1500 || snap.TotalPaid != 1090 {
		t.Fatalf("after top-up: %+v", snap)
	}
}

// 保额减少：退还按剩余天数占比向下取整。
func TestSumInsuredDecreaseRefundFloor(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy(baseInput("p")))
	mustOK(t, e.PayPremium("p", 0, 1000))
	mustOK(t, e.ScheduleEndorsement("p", EndorsementInput{
		ID: "down", Type: EndorsementSumInsured, ApplyDay: 0, EffDay: 300, NewSumInsured: 5000,
	}))
	events, err := e.Advance("p", 300)
	mustOK(t, err)
	// 新保费 ceil(1000*5000/10000)=500，差额 -500，剩余 65 天，
	// 退还 floor(500*65/365)=89。
	if len(events) != 1 || events[0].Kind != EventEffective || events[0].Refund != 89 {
		t.Fatalf("want effective refund 89, got %+v", events)
	}
	snap := mustSnapshot(t, e, "p")
	if snap.SumInsured != 5000 || snap.Premium != 500 || snap.TotalPaid != 911 {
		t.Fatalf("after refund: %+v", snap)
	}
}

// 待补缴不阻塞其后已可生效的批改。
func TestPendingTopUpDoesNotBlockLater(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy(baseInput("p")))
	mustOK(t, e.PayPremium("p", 0, 1000))
	mustOK(t, e.ScheduleEndorsement("p", EndorsementInput{
		ID: "up", Type: EndorsementSumInsured, ApplyDay: 0, EffDay: 10, NewSumInsured: 15000,
	}))
	mustOK(t, e.ScheduleEndorsement("p", EndorsementInput{
		ID: "ben", Type: EndorsementBeneficiary, ApplyDay: 0, EffDay: 11, Beneficiary: "x",
	}))
	events, err := e.Advance("p", 11)
	mustOK(t, err)
	// 补缴 ceil(500*355/365)=487。
	if len(events) != 2 || events[0].Kind != EventPendingTopUp || events[0].TopUp != 487 ||
		events[1].Kind != EventEffective || events[1].EndorsementID != "ben" {
		t.Fatalf("unexpected events: %+v", events)
	}
	snap := mustSnapshot(t, e, "p")
	if snap.Beneficiary != "x" || snap.SumInsured != 10000 {
		t.Fatalf("later endorsement must take effect: %+v", snap)
	}
	paid, err := e.PayTopUp("p", "up")
	mustOK(t, err)
	if paid != 487 {
		t.Fatalf("want 487, got %d", paid)
	}
	if snap = mustSnapshot(t, e, "p"); snap.SumInsured != 15000 || snap.TotalPaid != 1487 {
		t.Fatalf("after top-up: %+v", snap)
	}
}

// 存在未生效批改（预约或待补缴）时整单退保被拒，须先撤销。
func TestSurrenderBlockedByPendingEndorsement(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy(baseInput("p")))
	mustOK(t, e.PayPremium("p", 0, 1000))
	mustOK(t, e.ScheduleEndorsement("p", EndorsementInput{
		ID: "ben", Type: EndorsementBeneficiary, ApplyDay: 0, EffDay: 10, Beneficiary: "x",
	}))
	mustCode(t, func() error { _, err := e.Surrender("p", 0); return err }(), ErrHasPendingEndorsement)
	mustOK(t, e.CancelEndorsement("p", "ben"))
	refund, err := e.Surrender("p", 0)
	mustOK(t, err)
	if refund != 970 { // 犹豫期内：1000-30
		t.Fatalf("want 970, got %d", refund)
	}
}

// 部分退保恰等于最低保额允许，低于则拒。
func TestPartialSurrenderExactlyMin(t *testing.T) {
	e := NewEngine()
	in := baseInput("p")
	in.MinSumInsured = 4000
	in.CashValueRatios = []int64{100}
	mustOK(t, e.RegisterPolicy(in))
	mustOK(t, e.PayPremium("p", 0, 1000))
	if _, err := e.Advance("p", 20); err != nil {
		t.Fatal(err)
	}
	// 减少到 3999 < 4000：低于最低保额。
	mustCode(t, func() error { _, err := e.PartialSurrender("p", 6001, 0); return err }(), ErrBelowMinSumInsured)
	// 减少到恰好 4000：允许，退还 floor(1000*6000/10000)=600。
	refund, err := e.PartialSurrender("p", 6000, 0)
	mustOK(t, err)
	if refund != 600 {
		t.Fatalf("want 600, got %d", refund)
	}
	snap := mustSnapshot(t, e, "p")
	if snap.SumInsured != 4000 || snap.Premium != 400 {
		t.Fatalf("want SI 4000 premium 400, got %+v", snap)
	}
}

// 犹豫期内不允许部分退保。
func TestPartialSurrenderInHesitation(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy(baseInput("p")))
	mustOK(t, e.PayPremium("p", 0, 1000))
	mustCode(t, func() error { _, err := e.PartialSurrender("p", 1000, 0); return err }(), ErrInHesitation)
}

// 并发撤销与生效同一预约批改：结果必须与两种串行先后之一完全一致。
func TestConcurrentCancelAndEffect(t *testing.T) {
	for i := 0; i < 200; i++ {
		e := NewEngine()
		mustOK(t, e.RegisterPolicy(baseInput("p")))
		mustOK(t, e.PayPremium("p", 0, 1000))
		mustOK(t, e.ScheduleEndorsement("p", EndorsementInput{
			ID: "e1", Type: EndorsementSumInsured, ApplyDay: 0, EffDay: 5, NewSumInsured: 5000,
		}))
		var wg sync.WaitGroup
		var cancelErr error
		var events []Event
		var advErr error
		wg.Add(2)
		go func() { defer wg.Done(); cancelErr = e.CancelEndorsement("p", "e1") }()
		go func() { defer wg.Done(); events, advErr = e.Advance("p", 5) }()
		wg.Wait()
		mustOK(t, advErr)
		snap := mustSnapshot(t, e, "p")
		cancelFirst := cancelErr == nil && len(events) == 0 &&
			snap.Premium == 1000 && snap.TotalPaid == 1000 &&
			len(snap.Endorsements) == 0
		effectFirst := cancelErr != nil && len(events) == 1 &&
			events[0].Kind == EventEffective && events[0].Refund == 493 &&
			snap.Premium == 500 && snap.TotalPaid == 507 &&
			snap.Endorsements["e1"].State == StateEffective
		if cancelFirst == effectFirst {
			t.Fatalf("iter %d: outcome matches neither/both serial orders: cancelErr=%v events=%+v snap=%+v",
				i, cancelErr, events, snap)
		}
		if cancelErr != nil {
			mustCode(t, cancelErr, ErrAlreadyEffective)
		}
	}
}

// 拒绝次序逐对验证：同时满足多条拒绝条件时只报次序最前者。
func TestRejectionOrder(t *testing.T) {
	// 参数非法 > 保单不存在。
	e := NewEngine()
	mustCode(t, e.PayPremium("ghost", -1, 100), ErrInvalidParam)
	// 保单不存在 > 时钟回退（不存在的保单无时钟可言）。
	mustCode(t, func() error { _, err := e.Advance("ghost", 5); return err }(), ErrPolicyNotFound)

	// 时钟回退 > 已终态。
	e2 := NewEngine()
	in := baseInput("p")
	in.EffectiveDate = 10
	mustOK(t, e2.RegisterPolicy(in))
	if _, err := e2.Surrender("p", 0); err != nil {
		t.Fatal(err)
	}
	mustCode(t, func() error { _, err := e2.Advance("p", 9); return err }(), ErrClockRegression)
	// 已终态 > 批改不存在。
	mustCode(t, e2.CancelEndorsement("p", "ghost"), ErrTerminated)

	// 批改不存在。
	e3 := NewEngine()
	mustOK(t, e3.RegisterPolicy(baseInput("p")))
	mustCode(t, e3.CancelEndorsement("p", "ghost"), ErrEndorsementNotFound)

	// 批改重复 > 追溯批改。
	if _, err := e3.Advance("p", 10); err != nil {
		t.Fatal(err)
	}
	mustOK(t, e3.ScheduleEndorsement("p", EndorsementInput{
		ID: "e1", Type: EndorsementBeneficiary, ApplyDay: 10, EffDay: 10, Beneficiary: "a",
	}))
	mustCode(t, e3.ScheduleEndorsement("p", EndorsementInput{
		ID: "e1", Type: EndorsementBeneficiary, ApplyDay: 10, EffDay: 5, Beneficiary: "b",
	}), ErrEndorsementDuplicate)

	// 已生效（撤销已生效批改）。
	if _, err := e3.Advance("p", 10); err != nil {
		t.Fatal(err)
	}
	mustCode(t, e3.CancelEndorsement("p", "e1"), ErrAlreadyEffective)

	// 追溯批改：生效日早于当前时刻 / 早于申请日 / 早于已生效最晚生效日。
	mustCode(t, e3.ScheduleEndorsement("p", EndorsementInput{
		ID: "r1", Type: EndorsementBeneficiary, ApplyDay: 10, EffDay: 9,
	}), ErrRetroactive)
	mustCode(t, e3.ScheduleEndorsement("p", EndorsementInput{
		ID: "r2", Type: EndorsementBeneficiary, ApplyDay: 12, EffDay: 11,
	}), ErrRetroactive)

	// 待补缴（撤销待补缴批改）。
	e4 := NewEngine()
	mustOK(t, e4.RegisterPolicy(baseInput("p")))
	mustOK(t, e4.PayPremium("p", 0, 1000))
	mustOK(t, e4.ScheduleEndorsement("p", EndorsementInput{
		ID: "up", Type: EndorsementSumInsured, ApplyDay: 0, EffDay: 10, NewSumInsured: 15000,
	}))
	if _, err := e4.Advance("p", 10); err != nil {
		t.Fatal(err)
	}
	mustCode(t, e4.CancelEndorsement("p", "up"), ErrPendingTopUp)

	// 存在未生效批改 > 低于最低保额。
	e5 := NewEngine()
	in5 := baseInput("p")
	in5.MinSumInsured = 4000
	mustOK(t, e5.RegisterPolicy(in5))
	mustOK(t, e5.PayPremium("p", 0, 1000))
	if _, err := e5.Advance("p", 20); err != nil {
		t.Fatal(err)
	}
	mustOK(t, e5.ScheduleEndorsement("p", EndorsementInput{
		ID: "ben", Type: EndorsementBeneficiary, ApplyDay: 20, EffDay: 30, Beneficiary: "a",
	}))
	mustCode(t, func() error { _, err := e5.PartialSurrender("p", 9999, 0); return err }(), ErrHasPendingEndorsement)

	// 参数非法 > 已缴；已缴。
	e6 := NewEngine()
	mustOK(t, e6.RegisterPolicy(baseInput("p")))
	mustOK(t, e6.PayPremium("p", 0, 1000))
	mustCode(t, e6.PayPremium("p", 0, 999), ErrInvalidParam)
	mustCode(t, e6.PayPremium("p", 0, 1000), ErrAlreadyPaid)
	mustCode(t, e6.PayPremium("p", 2, 1000), ErrInvalidParam) // 只能缴当前或下一年度

	// 低于最低保额。
	e7 := NewEngine()
	in7 := baseInput("p")
	in7.MinSumInsured = 4000
	mustOK(t, e7.RegisterPolicy(in7))
	mustOK(t, e7.PayPremium("p", 0, 1000))
	if _, err := e7.Advance("p", 20); err != nil {
		t.Fatal(err)
	}
	mustCode(t, func() error { _, err := e7.PartialSurrender("p", 6001, 0); return err }(), ErrBelowMinSumInsured)
}

// 被拒绝的操作不得改变保单账、批改状态与当前时刻。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	e := NewEngine()
	mustOK(t, e.RegisterPolicy(baseInput("p")))
	mustOK(t, e.PayPremium("p", 0, 1000))
	mustOK(t, e.ScheduleEndorsement("p", EndorsementInput{
		ID: "ben", Type: EndorsementBeneficiary, ApplyDay: 0, EffDay: 10, Beneficiary: "x",
	}))
	if _, err := e.Advance("p", 5); err != nil {
		t.Fatal(err)
	}
	before := mustSnapshot(t, e, "p")
	rejected := []error{
		e.PayPremium("p", 0, 1000),        // 已缴
		e.PayPremium("p", 3, 1000),        // 参数非法
		e.PayPremium("ghost", 0, 1000),    // 保单不存在
		e.CancelEndorsement("p", "ghost"), // 批改不存在
		e.ScheduleEndorsement("p", EndorsementInput{ID: "ben", Type: EndorsementBeneficiary, ApplyDay: 5, EffDay: 9}), // 批改重复
		e.ScheduleEndorsement("p", EndorsementInput{ID: "r", Type: EndorsementBeneficiary, ApplyDay: 5, EffDay: 4}),   // 追溯批改
		func() error { _, err := e.Surrender("p", 0); return err }(),                                                  // 存在未生效批改
		func() error { _, err := e.PartialSurrender("p", 9999, 0); return err }(),                                     // 犹豫期内
		func() error { _, err := e.Advance("p", 4); return err }(),                                                    // 时钟回退
		func() error { _, err := e.PayTopUp("p", "ben"); return err }(),                                               // 参数非法
	}
	for i, err := range rejected {
		if err == nil {
			t.Fatalf("op %d should be rejected", i)
		}
	}
	after := mustSnapshot(t, e, "p")
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("rejected ops left trace:\nbefore=%+v\nafter=%+v", before, after)
	}
}

// 相同操作序列重放得到完全相同的结果。
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]any, Snapshot) {
		e := NewEngine()
		var out []any
		mustOK(t, e.RegisterPolicy(baseInput("p")))
		out = append(out, e.PayPremium("p", 0, 1000))
		out = append(out, e.ScheduleEndorsement("p", EndorsementInput{
			ID: "up", Type: EndorsementSumInsured, ApplyDay: 0, EffDay: 100, NewSumInsured: 20000,
		}))
		out = append(out, e.ScheduleEndorsement("p", EndorsementInput{
			ID: "ben", Type: EndorsementBeneficiary, ApplyDay: 0, EffDay: 100, Beneficiary: "z",
		}))
		events, err := e.Advance("p", 100)
		out = append(out, events, err)
		paid, err := e.PayTopUp("p", "up")
		out = append(out, paid, err)
		cv, err := e.CashValue("p", 100, 5)
		out = append(out, cv, err)
		refund, err := e.PartialSurrender("p", 5000, 5)
		out = append(out, refund, err)
		refund2, err := e.Surrender("p", 5)
		out = append(out, refund2, err)
		return out, mustSnapshot(t, e, "p")
	}
	out1, snap1 := run()
	out2, snap2 := run()
	if !reflect.DeepEqual(out1, out2) || !reflect.DeepEqual(snap1, snap2) {
		t.Fatalf("replay mismatch:\n%v\n%v", out1, out2)
	}
}
