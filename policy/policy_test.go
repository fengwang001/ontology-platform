package policy

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// baseConfig 返回基准配置：应缴日序列 0,30,60,...，宽限期 [30,39]，
// 宽限期满次日（中止起算日）40，复效期 [40,59]，终止日 60。
func baseConfig() Config {
	return Config{
		EffectiveDay:    0,
		PeriodDays:      30,
		PremiumCents:    1000,
		GraceDays:       10,
		ReinstateDays:   20,
		WaitingDays:     0,
		SumAssuredCents: 100000,
		CashValues:      []int64{0},
	}
}

func mustRegister(t *testing.T, e *Engine, cfg Config) uint64 {
	t.Helper()
	id, err := e.Register(cfg)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return id
}

func mustQuery(t *testing.T, e *Engine, id uint64, day int64) Snapshot {
	t.Helper()
	snap, err := e.Query(id, day)
	if err != nil {
		t.Fatalf("Query(%d): %v", day, err)
	}
	return snap
}

func TestPayOnDueDay(t *testing.T) {
	e := NewEngine()
	id := mustRegister(t, e, baseConfig())
	// 应缴日当天（第 2 期应缴日 30）未缴即宽限中
	if snap := mustQuery(t, e, id, 30); snap.State != StateGrace || snap.OwedCents != 1000 {
		t.Fatalf("due day: got %+v", snap)
	}
	// 应缴日当天缴费，回到有效
	if err := e.PayPremium(id, 30, 1000); err != nil {
		t.Fatalf("PayPremium: %v", err)
	}
	snap := mustQuery(t, e, id, 30)
	if snap.State != StateActive || snap.PaidPeriods != 2 || snap.NextDueDay != 60 || snap.OwedCents != 0 {
		t.Fatalf("after pay: got %+v", snap)
	}
}

func TestPayOnGraceLastDay(t *testing.T) {
	e := NewEngine()
	id := mustRegister(t, e, baseConfig())
	// 宽限期末日（39）当天仍可缴
	if err := e.PayPremium(id, 39, 1000); err != nil {
		t.Fatalf("PayPremium: %v", err)
	}
	snap := mustQuery(t, e, id, 39)
	if snap.State != StateActive || snap.NextDueDay != 60 {
		t.Fatalf("got %+v", snap)
	}
}

func TestClaimOnGraceLastDay(t *testing.T) {
	e := NewEngine()
	id := mustRegister(t, e, baseConfig())
	// 出险日恰为中止起算日前一天（宽限期末日 39），仍按宽限中处理
	res, err := e.Claim(id, 39)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	want := ClaimResult{Pay: true, AmountCents: 100000 - 1000, Reason: ReasonGraceDeducted}
	if res != want {
		t.Fatalf("got %+v want %+v", res, want)
	}
}

func TestClaimAfterGraceExpiry(t *testing.T) {
	e := NewEngine()
	id := mustRegister(t, e, baseConfig())
	// 宽限期满次日（40）出险：现金价值净值为 0 不足垫交，已中止，不赔付
	res, err := e.Claim(id, 40)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if res != (ClaimResult{Pay: false, AmountCents: 0, Reason: ReasonLapsed}) {
		t.Fatalf("got %+v", res)
	}
	if snap := mustQuery(t, e, id, 40); snap.State != StateLapsed {
		t.Fatalf("got %+v", snap)
	}
}

func TestAutoLoanWhenNetEqualsPremium(t *testing.T) {
	e := NewEngine()
	cfg := baseConfig()
	cfg.CashValues = []int64{0, 1000} // 净值恰等于一期保费
	id := mustRegister(t, e, cfg)
	snap := mustQuery(t, e, id, 40)
	if snap.State != StateActive || snap.LoanPrincipal != 1000 || snap.LoanCount != 1 ||
		snap.PaidPeriods != 2 || snap.NextDueDay != 60 {
		t.Fatalf("got %+v", snap)
	}
}

func TestLapseWhenNetOneCentShort(t *testing.T) {
	e := NewEngine()
	cfg := baseConfig()
	cfg.CashValues = []int64{0, 999} // 净值比一期保费少 1 分
	id := mustRegister(t, e, cfg)
	if snap := mustQuery(t, e, id, 40); snap.State != StateLapsed || snap.LoanCount != 0 {
		t.Fatalf("got %+v", snap)
	}
	if snap := mustQuery(t, e, id, 59); snap.State != StateLapsed {
		t.Fatalf("got %+v", snap)
	}
	if snap := mustQuery(t, e, id, 60); snap.State != StateTerminated {
		t.Fatalf("got %+v", snap)
	}
}

func TestChainedAutoLoansUntilInsolvent(t *testing.T) {
	e := NewEngine()
	cfg := baseConfig()
	cfg.CashValues = []int64{0, 3500, 3500, 3500, 3500}
	id := mustRegister(t, e, cfg)
	// 第 40/70/100 天各垫交一次，第 130 天净值 500 不足，中止
	checks := []struct {
		day       int64
		state     State
		principal int64
		paid      int64
	}{
		{40, StateActive, 1000, 2},
		{70, StateActive, 2000, 3},
		{100, StateActive, 3000, 4},
		{130, StateLapsed, 3000, 4},
	}
	for _, c := range checks {
		snap := mustQuery(t, e, id, c.day)
		if snap.State != c.state || snap.LoanPrincipal != c.principal || snap.PaidPeriods != c.paid {
			t.Fatalf("day %d: got %+v", c.day, snap)
		}
		if snap.LoanCount != c.paid-1 {
			t.Fatalf("day %d: loan count %+v", c.day, snap)
		}
	}
	// 中止起算日 130，复效期 20 天，第 150 天终止
	if snap := mustQuery(t, e, id, 150); snap.State != StateTerminated {
		t.Fatalf("got %+v", snap)
	}
}

func TestReinstateOnLastDayAndRejectsNextDay(t *testing.T) {
	// 复效期末日（59）申请成功
	e := NewEngine()
	id := mustRegister(t, e, baseConfig())
	if err := e.Advance(id, 50); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := e.Reinstate(id, 59, 1000); err != nil {
		t.Fatalf("Reinstate on last day: %v", err)
	}
	snap := mustQuery(t, e, id, 59)
	if snap.State != StateActive || snap.PaidPeriods != 2 || snap.NextDueDay != 89 ||
		snap.LoanPrincipal != 0 || snap.LoanInterest != 0 {
		t.Fatalf("after reinstate: got %+v", snap)
	}
	// 复效期满次日（60）已终止，申请报「状态不允许」
	e2 := NewEngine()
	id2 := mustRegister(t, e2, baseConfig())
	if err := e2.Advance(id2, 60); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if snap := mustQuery(t, e2, id2, 60); snap.State != StateTerminated {
		t.Fatalf("got %+v", snap)
	}
	if err := e2.Reinstate(id2, 60, 1000); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("want ErrStateNotAllowed, got %v", err)
	}
}

func TestReinstateInsufficientByOneCent(t *testing.T) {
	e := NewEngine()
	id := mustRegister(t, e, baseConfig())
	if err := e.Advance(id, 50); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	before := mustQuery(t, e, id, 50)
	if before.State != StateLapsed {
		t.Fatalf("got %+v", before)
	}
	// 应补 1000，差 1 分报「补缴不足」且不留痕
	if err := e.Reinstate(id, 50, 999); !errors.Is(err, ErrInsufficientPayment) {
		t.Fatalf("want ErrInsufficientPayment, got %v", err)
	}
	if after := mustQuery(t, e, id, 50); after != before {
		t.Fatalf("rejected op left trace: before %+v after %+v", before, after)
	}
	if err := e.Reinstate(id, 50, 1000); err != nil {
		t.Fatalf("Reinstate exact: %v", err)
	}
	if snap := mustQuery(t, e, id, 50); snap.State != StateActive {
		t.Fatalf("got %+v", snap)
	}
}

func TestInterestRoundingUp(t *testing.T) {
	cfg := baseConfig()
	cfg.LoanRatePerMyriad = 11 // 本金 1000 时单日利息 = ceil(1000*11/10000) = ceil(1.1) = 2 分
	cfg.CashValues = []int64{0, 100000, 100000}
	e := NewEngine()
	id := mustRegister(t, e, cfg)
	// 第 40 天宽限期满自动垫交 1000
	snap := mustQuery(t, e, id, 40)
	if snap.State != StateActive || snap.LoanPrincipal != 1000 || snap.LoanInterest != 0 {
		t.Fatalf("day 40: got %+v", snap)
	}
	if snap = mustQuery(t, e, id, 41); snap.LoanInterest != 2 {
		t.Fatalf("day 41 interest: got %+v", snap)
	}
	if snap = mustQuery(t, e, id, 45); snap.LoanInterest != 10 {
		t.Fatalf("day 45 interest: got %+v", snap)
	}
}

func TestRepayInterestFirstThenPrincipal(t *testing.T) {
	cfg := baseConfig()
	cfg.LoanRatePerMyriad = 10 // 单日利息恰为 1 分
	cfg.CashValues = []int64{0, 100000, 100000}
	e := NewEngine()
	id := mustRegister(t, e, cfg)
	if err := e.Advance(id, 45); err != nil { // 本金 1000，利息 5
		t.Fatalf("Advance: %v", err)
	}
	if err := e.RepayLoan(id, 45, 3); err != nil {
		t.Fatalf("RepayLoan: %v", err)
	}
	if snap := mustQuery(t, e, id, 45); snap.LoanInterest != 2 || snap.LoanPrincipal != 1000 {
		t.Fatalf("interest first: got %+v", snap)
	}
	if err := e.RepayLoan(id, 45, 402); err != nil { // 2 分利息 + 400 分本金
		t.Fatalf("RepayLoan: %v", err)
	}
	if snap := mustQuery(t, e, id, 45); snap.LoanInterest != 0 || snap.LoanPrincipal != 600 {
		t.Fatalf("then principal: got %+v", snap)
	}
	if err := e.RepayLoan(id, 45, 601); !errors.Is(err, ErrOverpayment) {
		t.Fatalf("want ErrOverpayment, got %v", err)
	}
	if err := e.RepayLoan(id, 45, 600); err != nil {
		t.Fatalf("RepayLoan: %v", err)
	}
	if snap := mustQuery(t, e, id, 45); snap.LoanPrincipal != 0 || snap.LoanInterest != 0 {
		t.Fatalf("cleared: got %+v", snap)
	}
}

func TestRepayOnlyInActiveState(t *testing.T) {
	cfg := baseConfig()
	cfg.CashValues = []int64{0, 5000, 5000, 5000}
	e := NewEngine()
	id := mustRegister(t, e, cfg)
	if err := e.Advance(id, 65); err != nil { // 第 40 天垫交，第 60 天起又入宽限
		t.Fatalf("Advance: %v", err)
	}
	if snap := mustQuery(t, e, id, 65); snap.State != StateGrace || snap.LoanPrincipal != 1000 {
		t.Fatalf("got %+v", snap)
	}
	if err := e.RepayLoan(id, 65, 100); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("want ErrStateNotAllowed, got %v", err)
	}
}

func TestMultiPeriodPaymentExtendsDueDay(t *testing.T) {
	e := NewEngine()
	id := mustRegister(t, e, baseConfig())
	if err := e.PayPremium(id, 0, 3000); err != nil {
		t.Fatalf("PayPremium: %v", err)
	}
	snap := mustQuery(t, e, id, 100)
	if snap.State != StateActive || snap.PaidPeriods != 4 || snap.NextDueDay != 120 {
		t.Fatalf("got %+v", snap)
	}
}

func TestPayRejectedInLapsedAndTerminated(t *testing.T) {
	e := NewEngine()
	id := mustRegister(t, e, baseConfig())
	if err := e.Advance(id, 50); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := e.PayPremium(id, 50, 1000); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("lapsed: want ErrStateNotAllowed, got %v", err)
	}
	if err := e.Advance(id, 60); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := e.PayPremium(id, 60, 1000); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("terminated: want ErrStateNotAllowed, got %v", err)
	}
}

func TestAdvanceAcrossManyPeriods(t *testing.T) {
	cfg := baseConfig()
	cfg.CashValues = []int64{0, 2500, 2500, 2500, 2500}
	e := NewEngine()
	id := mustRegister(t, e, cfg)
	// 一次推进 500 天：第 40、70 天各垫交一次，第 100 天净值 500 不足而中止，
	// 第 120 天复效期满终止。
	if err := e.Advance(id, 500); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	snap := mustQuery(t, e, id, 500)
	want := Snapshot{
		Day: 500, State: StateTerminated, PaidPeriods: 3, NextDueDay: 90,
		OwedCents: 0, LoanPrincipal: 2000, LoanInterest: 0, LoanCount: 2,
		WaitingUntil: 0,
	}
	if snap != want {
		t.Fatalf("got %+v want %+v", snap, want)
	}
}

func TestWaitingPeriodAfterReinstate(t *testing.T) {
	cfg := baseConfig()
	cfg.WaitingDays = 5
	e := NewEngine()
	id := mustRegister(t, e, cfg)
	if err := e.Advance(id, 50); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := e.Reinstate(id, 50, 1000); err != nil {
		t.Fatalf("Reinstate: %v", err)
	}
	// 复效生效日 50 起 5 天等待期 [50,54]
	res, err := e.Claim(id, 54)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if res != (ClaimResult{Pay: false, AmountCents: 0, Reason: ReasonWaiting}) {
		t.Fatalf("waiting claim: got %+v", res)
	}
	res, err = e.Claim(id, 55)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if res != (ClaimResult{Pay: true, AmountCents: 100000, Reason: ReasonFull}) {
		t.Fatalf("after waiting: got %+v", res)
	}
	// 等待期不赔付与中止不赔付可区分
	if ReasonWaiting == ReasonLapsed {
		t.Fatal("reasons must be distinguishable")
	}
}

func TestInitialWaitingPeriod(t *testing.T) {
	cfg := baseConfig()
	cfg.WaitingDays = 5
	e := NewEngine()
	id := mustRegister(t, e, cfg)
	res, err := e.Claim(id, 4)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if res.Reason != ReasonWaiting || res.Pay {
		t.Fatalf("got %+v", res)
	}
	res, err = e.Claim(id, 5)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !res.Pay || res.Reason != ReasonFull {
		t.Fatalf("got %+v", res)
	}
}

// TestRejectionOrder 逐对验证固定拒绝次序：
// 参数非法 > 保单不存在 > 时钟回退 > 状态不允许 > 补缴不足 > 超额还款。
func TestRejectionOrder(t *testing.T) {
	e := NewEngine()
	id := mustRegister(t, e, baseConfig())
	if err := e.Advance(id, 10); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	// 参数非法 > 保单不存在
	if err := e.PayPremium(9999, -1, 1000); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("got %v", err)
	}
	// 保单不存在 > 时钟回退（保单不存在时无所谓时刻）
	if err := e.PayPremium(9999, 5, 1000); !errors.Is(err, ErrPolicyNotFound) {
		t.Fatalf("got %v", err)
	}
	// 参数非法（非整数期保费）> 时钟回退
	if err := e.PayPremium(id, 5, 1500); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("got %v", err)
	}
	// 时钟回退 > 状态不允许（保单已中止，且时刻回退）
	if err := e.Advance(id, 50); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if snap := mustQuery(t, e, id, 50); snap.State != StateLapsed {
		t.Fatalf("got %+v", snap)
	}
	if err := e.PayPremium(id, 49, 1000); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("got %v", err)
	}
	// 状态不允许 > 补缴不足（有效保单申请复效，金额再低也先报状态）
	e2 := NewEngine()
	id2 := mustRegister(t, e2, baseConfig())
	if err := e2.Reinstate(id2, 0, 1); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("got %v", err)
	}
	// 状态不允许 > 超额还款（宽限中还款，金额再大也先报状态）
	cfg := baseConfig()
	cfg.CashValues = []int64{0, 5000, 5000, 5000}
	e3 := NewEngine()
	id3 := mustRegister(t, e3, cfg)
	if err := e3.Advance(id3, 65); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := e3.RepayLoan(id3, 65, 1<<40); !errors.Is(err, ErrStateNotAllowed) {
		t.Fatalf("got %v", err)
	}
	// 补缴不足、超额还款各自可触发且可区分
	if err := e.Reinstate(id, 50, 999); !errors.Is(err, ErrInsufficientPayment) {
		t.Fatalf("got %v", err)
	}
	e4 := NewEngine()
	id4 := mustRegister(t, e4, cfg)
	if err := e4.Advance(id4, 41); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := e4.RepayLoan(id4, 41, 1<<40); !errors.Is(err, ErrOverpayment) {
		t.Fatalf("got %v", err)
	}
}

// TestRejectedOpsLeaveNoTrace 各类被拒操作不得改变状态、借款账、应缴日与当前时刻。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cfg := baseConfig()
	cfg.LoanRatePerMyriad = 7
	cfg.CashValues = []int64{0, 5000, 5000, 5000}
	e := NewEngine()
	id := mustRegister(t, e, cfg)
	if err := e.Advance(id, 45); err != nil { // 有效，有一笔垫交借款与利息
		t.Fatalf("Advance: %v", err)
	}
	before := mustQuery(t, e, id, 45)
	rejected := []func() error{
		func() error { return e.PayPremium(id, 45, 1500) },   // 参数非法
		func() error { return e.PayPremium(id, 44, 1000) },   // 时钟回退
		func() error { return e.RepayLoan(id, 45, 1<<40) },   // 超额还款
		func() error { return e.Reinstate(id, 45, 10) },      // 状态不允许（非中止）
		func() error { return e.PayPremium(9999, 45, 1000) }, // 保单不存在
	}
	for i, op := range rejected {
		if err := op(); err == nil {
			t.Fatalf("op %d should be rejected", i)
		}
		if after := mustQuery(t, e, id, 45); after != before {
			t.Fatalf("op %d left trace: before %+v after %+v", i, before, after)
		}
	}
	// 中止保单上「补缴不足」也不留痕
	e2 := NewEngine()
	id2 := mustRegister(t, e2, baseConfig())
	if err := e2.Advance(id2, 50); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	before2 := mustQuery(t, e2, id2, 50)
	if err := e2.Reinstate(id2, 50, 1); !errors.Is(err, ErrInsufficientPayment) {
		t.Fatalf("got %v", err)
	}
	if after := mustQuery(t, e2, id2, 50); after != before2 {
		t.Fatalf("insufficient reinstate left trace: before %+v after %+v", before2, after)
	}
}

// TestDeterministicReplay 相同操作序列重放得到完全相同的状态轨迹。
func TestDeterministicReplay(t *testing.T) {
	cfg := baseConfig()
	cfg.LoanRatePerMyriad = 3
	cfg.CashValues = []int64{0, 1500, 1500, 1500}
	run := func() []Snapshot {
		e := NewEngine()
		id, _ := e.Register(cfg)
		var trace []Snapshot
		_ = e.Advance(id, 40)
		trace = append(trace, mustQuery(t, e, id, 40))
		_ = e.Advance(id, 70)
		trace = append(trace, mustQuery(t, e, id, 70))
		_ = e.RepayLoan(id, 70, 50) // 中止状态不允许
		_ = e.Reinstate(id, 75, 10) // 补缴不足
		trace = append(trace, mustQuery(t, e, id, 75))
		// 第 75 天应补：欠费 1000 + 本金 1000 + 利息 ceil(1000*3/10000)=1/天*35 天 = 35
		if err := e.Reinstate(id, 75, 2035); err != nil {
			t.Fatalf("Reinstate: %v", err)
		}
		trace = append(trace, mustQuery(t, e, id, 75))
		_ = e.Advance(id, 200)
		trace = append(trace, mustQuery(t, e, id, 200))
		return trace
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch:\n%v\n%v", first, second)
	}
	// 顺便校验复效金额计算精确到分
	if first[2].LoanPrincipal != 1000 || first[2].LoanInterest != 35 {
		t.Fatalf("loan at day 75: %+v", first[2])
	}
	if first[3].State != StateActive || first[3].LoanPrincipal != 0 || first[3].NextDueDay != 105 {
		t.Fatalf("after reinstate: %+v", first[3])
	}
}

// TestSettleStepsBounded 证明推进与查询开销不随历史期数与借款笔数增长。
func TestSettleStepsBounded(t *testing.T) {
	cfg := Config{
		EffectiveDay: 0, PeriodDays: 5, PremiumCents: 100, GraceDays: 2,
		ReinstateDays: 3, SumAssuredCents: 10000, CashValues: []int64{0, 1 << 40},
	}
	e := NewEngine()
	id := mustRegister(t, e, cfg)
	p := e.policies[id]
	// 逐日推进 7000 天，积累约 1000 期缴费与 1000 笔垫交借款
	for d := int64(1); d <= 7000; d++ {
		before := p.settleSteps
		if err := e.Advance(id, d); err != nil {
			t.Fatalf("Advance(%d): %v", d, err)
		}
		if got := p.settleSteps - before; got > 2 {
			t.Fatalf("day %d: settle steps %d > 2", d, got)
		}
	}
	snap := mustQuery(t, e, id, 7000)
	if snap.PaidPeriods < 1000 || snap.LoanCount < 900 {
		t.Fatalf("history too short for proof: %+v", snap)
	}
	// 历史已很长，单日推进开销仍为常数
	before := p.settleSteps
	if err := e.Advance(id, 7001); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got := p.settleSteps - before; got > 2 {
		t.Fatalf("steps grow with history: %d", got)
	}
	// 对齐到事件日（垫交每 5 天一次，事件日 ≡2 mod 5），再跨 700 天推进：
	// 开销正比于窗口内事件数（140 次垫交），而非历史（1400+ 期、1400+ 笔借款）
	if err := e.Advance(id, 7002); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	before = p.settleSteps
	if err := e.Advance(id, 7702); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	got := p.settleSteps - before
	// 对照：全新保单推进相位对齐的等长窗口，事件数相同，步数必须完全一致
	fresh := NewEngine()
	fid := mustRegister(t, fresh, cfg)
	fp := fresh.policies[fid]
	if err := fresh.Advance(fid, 7); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	freshBefore := fp.settleSteps
	if err := fresh.Advance(fid, 707); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if want := fp.settleSteps - freshBefore; got != want {
		t.Fatalf("steps grow with history: got %d, fresh-policy window %d", got, want)
	}
}

// TestConcurrent 并发调用等价于某个串行顺序：每个 goroutine 独占一张保单时，
// 结果必须与串行执行完全一致；共享保单上的并发只读查询不得竞态。
func TestConcurrent(t *testing.T) {
	const workers = 8
	script := func(e *Engine, id uint64) []Snapshot {
		var out []Snapshot
		for d := int64(1); d <= 120; d++ {
			switch {
			case d%10 == 0:
				_ = e.PayPremium(id, d, 1000)
			case d%7 == 0:
				_, _ = e.Claim(id, d)
			default:
				_ = e.Advance(id, d)
			}
			if d%3 == 0 {
				snap, _ := e.Query(id, d)
				out = append(out, snap)
			}
		}
		return out
	}
	// 串行基准
	want := make([][]Snapshot, workers)
	for g := 0; g < workers; g++ {
		e := NewEngine()
		id, _ := e.Register(baseConfig())
		want[g] = script(e, id)
	}
	// 并发执行
	e := NewEngine()
	shared := mustRegister(t, e, baseConfig())
	ids := make([]uint64, workers)
	for g := range ids {
		ids[g] = mustRegister(t, e, baseConfig())
	}
	got := make([][]Snapshot, workers)
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			got[g] = script(e, ids[g])
			_, _ = e.Query(shared, 0) // 共享保单上的并发只读
			_, _ = e.Claim(shared, 0)
		}(g)
	}
	wg.Wait()
	if !reflect.DeepEqual(got, want) {
		t.Fatal("concurrent results differ from serial execution")
	}
}
