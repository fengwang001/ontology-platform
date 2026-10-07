package policy

import (
	"sync"
	"testing"
)

// baseConfig 现金价值恒 0（永不垫交），利率 0，便于手算。
// 应缴日：第 2 期 = 30；宽限 30..39；中止起算日 40；复效期末日 59；终止日 60。
func baseConfig() Config {
	return Config{
		EffectiveDay: 0,
		PeriodDays:   30,
		Premium:      1000,
		GraceDays:    10,
		RevivalDays:  20,
		WaitingDays:  5,
		CashValue:    []int64{0, 0, 0},
		DailyRatePPM: 0,
	}
}

func mustRegister(t *testing.T, e *Engine, id string, cfg Config) {
	t.Helper()
	if err := e.Register(id, cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

func mustQuery(t *testing.T, e *Engine, id string, day int) Snapshot {
	t.Helper()
	s, err := e.Query(id, day)
	if err != nil {
		t.Fatalf("Query(%d): %v", day, err)
	}
	return s
}

func mustClaim(t *testing.T, e *Engine, id string, day int) ClaimResult {
	t.Helper()
	r, err := e.Claim(id, day)
	if err != nil {
		t.Fatalf("Claim(%d): %v", day, err)
	}
	return r
}

// 应缴日当天缴费：应缴日已处于宽限中，缴费后回到有效。
func TestPayOnDueDay(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "p", baseConfig())
	if s := mustQuery(t, e, "p", 30); s.State != StateGrace || s.GraceDueDay != 30 {
		t.Fatalf("应缴日应为宽限中: %+v", s)
	}
	if err := e.PayPremium("p", 30, 1000); err != nil {
		t.Fatalf("PayPremium: %v", err)
	}
	s := mustQuery(t, e, "p", 30)
	if s.State != StateActive || s.PaidCount != 2 || s.NextDueDay != 60 {
		t.Fatalf("缴费后状态错误: %+v", s)
	}
}

// 宽限期末日缴费：仍可缴，且不产生借款。
func TestPayOnGraceLastDay(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "p", baseConfig())
	if err := e.PayPremium("p", 39, 1000); err != nil {
		t.Fatalf("PayPremium: %v", err)
	}
	s := mustQuery(t, e, "p", 39)
	if s.State != StateActive || s.PaidCount != 2 || s.LoanPrincipal != 0 {
		t.Fatalf("宽限期末日缴费后状态错误: %+v", s)
	}
}

// 宽限期末日出险：按宽限中处理，赔付全额减去该期欠缴保费。
func TestClaimOnGraceLastDay(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "p", baseConfig())
	r := mustClaim(t, e, "p", 39)
	if r.Verdict != PayReduced || r.Deduction != 1000 {
		t.Fatalf("宽限期末日出险判定错误: %+v", r)
	}
}

// 宽限期满次日（即中止起算日）出险：中止不赔付。
func TestClaimOnLapseDay(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "p", baseConfig())
	if r := mustClaim(t, e, "p", 40); r.Verdict != DenyLapsed {
		t.Fatalf("中止起算日出险判定错误: %+v", r)
	}
	if s := mustQuery(t, e, "p", 40); s.State != StateLapsed || s.LapseDay != 40 {
		t.Fatalf("中止状态错误: %+v", s)
	}
}

// 净值恰等于一期保费时垫交（全有或全无的边界）。
func TestAutoLoanWhenNetEqualsPremium(t *testing.T) {
	e := NewEngine()
	cfg := baseConfig()
	cfg.CashValue = []int64{0, 1000, 1000}
	mustRegister(t, e, "p", cfg)
	s := mustQuery(t, e, "p", 40)
	if s.State != StateActive || s.PaidCount != 2 || s.LoanPrincipal != 1000 || s.NextDueDay != 60 {
		t.Fatalf("净值恰等于一期保费应垫交: %+v", s)
	}
}

// 净值比一期保费小 1 分时不垫交，进入中止。
func TestNoAutoLoanWhenNetOneCentShort(t *testing.T) {
	e := NewEngine()
	cfg := baseConfig()
	cfg.CashValue = []int64{0, 999, 999}
	mustRegister(t, e, "p", cfg)
	s := mustQuery(t, e, "p", 40)
	if s.State != StateLapsed || s.LapseDay != 40 || s.LoanPrincipal != 0 {
		t.Fatalf("净值小 1 分应中止: %+v", s)
	}
}

// 连续多次垫交直至净值不足：周期 10、宽限 5、保费 100、现金价值 250。
// 垫交两次后本金 200，净值 250-200=50 < 100，于第 35 天中止。
func TestChainedAutoLoansUntilInsolvent(t *testing.T) {
	e := NewEngine()
	cfg := Config{
		EffectiveDay: 0, PeriodDays: 10, Premium: 100,
		GraceDays: 5, RevivalDays: 20, WaitingDays: 0,
		CashValue: []int64{0, 250, 250, 250}, DailyRatePPM: 0,
	}
	mustRegister(t, e, "p", cfg)
	s := mustQuery(t, e, "p", 40)
	if s.State != StateLapsed || s.LapseDay != 35 || s.PaidCount != 3 || s.LoanPrincipal != 200 {
		t.Fatalf("连续垫交后应中止: %+v", s)
	}
	if s := mustQuery(t, e, "p", 55); s.State != StateTerminated {
		t.Fatalf("复效期满应终止: %+v", s)
	}
}

// 复效期末日可申请复效，次日则保单已终止。
func TestReinstateDeadlineAndNextDay(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "a", baseConfig())
	// 复效期末日 59：复效成功。
	if err := e.Reinstate("a", 59, 1000); err != nil {
		t.Fatalf("复效期末日复效应成功: %v", err)
	}
	if s := mustQuery(t, e, "a", 59); s.State != StateActive || s.PaidCount != 2 {
		t.Fatalf("复效后状态错误: %+v", s)
	}
	// 另一张保单：次日 60 已终止，复效报状态不允许。
	mustRegister(t, e, "b", baseConfig())
	if err := e.Advance("b", 60); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := e.Reinstate("b", 60, 1000); err != ErrStateNotAllowed {
		t.Fatalf("终止后复效应报状态不允许, got %v", err)
	}
	if s := mustQuery(t, e, "b", 60); s.State != StateTerminated {
		t.Fatalf("复效期满次日应终止: %+v", s)
	}
}

// 补缴金额差 1 分报补缴不足，且不记入任何部分款项。
func TestReinstateOneCentShort(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "p", baseConfig())
	before := mustQuery(t, e, "p", 45) // 已中止，需补缴 1000
	if before.State != StateLapsed || before.OwedAmount != 1000 {
		t.Fatalf("前置状态错误: %+v", before)
	}
	if err := e.Reinstate("p", 45, 999); err != ErrInsufficientPayment {
		t.Fatalf("差 1 分应报补缴不足, got %v", err)
	}
	if after := mustQuery(t, e, "p", 45); after != before {
		t.Fatalf("被拒复效不得留痕: %+v -> %+v", before, after)
	}
	if err := e.Reinstate("p", 45, 1000); err != nil {
		t.Fatalf("足额补缴应成功: %v", err)
	}
}

// 利息按日单利逐日累计、按分向上取整。
func TestInterestRounding(t *testing.T) {
	e := NewEngine()
	cfg := baseConfig()
	cfg.WaitingDays = 0
	cfg.DailyRatePPM = 15 // 每日利息 = ceil(1000*15/10000) = ceil(1.5) = 2 分
	cfg.CashValue = []int64{0, 100000, 100000}
	mustRegister(t, e, "p", cfg)
	if err := e.Advance("p", 40); err != nil { // 第 40 天垫交 1000
		t.Fatalf("Advance: %v", err)
	}
	s := mustQuery(t, e, "p", 43)
	if s.LoanPrincipal != 1000 || s.LoanInterest != 6 { // 3 天 * 2 分
		t.Fatalf("利息取整错误: %+v", s)
	}
	// 小额本金每日利息不足 1 分也向上取整为 1 分。
	cfg2 := cfg
	cfg2.Premium = 100
	cfg2.DailyRatePPM = 5 // ceil(100*5/10000) = ceil(0.05) = 1 分/天
	mustRegister(t, e, "q", cfg2)
	if err := e.Advance("q", 40); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if s := mustQuery(t, e, "q", 50); s.LoanInterest != 10 {
		t.Fatalf("小额利息取整错误: %+v", s)
	}
}

// 还款先还利息后还本金；超额还款报错且账不变。
func TestRepayInterestFirst(t *testing.T) {
	e := NewEngine()
	cfg := baseConfig()
	cfg.WaitingDays = 0
	cfg.DailyRatePPM = 15
	cfg.CashValue = []int64{0, 100000, 100000}
	mustRegister(t, e, "p", cfg)
	if err := e.Advance("p", 43); err != nil { // 垫交 1000，利息 6
		t.Fatalf("Advance: %v", err)
	}
	if err := e.RepayLoan("p", 43, 6); err != nil { // 恰好还完利息
		t.Fatalf("RepayLoan: %v", err)
	}
	s := mustQuery(t, e, "p", 43)
	if s.LoanInterest != 0 || s.LoanPrincipal != 1000 {
		t.Fatalf("先还利息错误: %+v", s)
	}
	if err := e.RepayLoan("p", 43, 400); err != nil { // 再还本金
		t.Fatalf("RepayLoan: %v", err)
	}
	if s := mustQuery(t, e, "p", 43); s.LoanPrincipal != 600 {
		t.Fatalf("后还本金错误: %+v", s)
	}
	if err := e.RepayLoan("p", 43, 601); err != ErrOverpayment {
		t.Fatalf("超额还款应报错, got %v", err)
	}
	if s := mustQuery(t, e, "p", 43); s.LoanPrincipal != 600 {
		t.Fatalf("被拒还款不得留痕: %+v", s)
	}
}

// 一次推进跨越多个周期：每个应缴、宽限期满、垫交事件都恰好结算一次。
func TestAdvanceAcrossManyPeriods(t *testing.T) {
	e := NewEngine()
	cfg := Config{
		EffectiveDay: 0, PeriodDays: 10, Premium: 100,
		GraceDays: 3, RevivalDays: 20, WaitingDays: 0,
		CashValue: []int64{0, 1000000000, 1000000000}, DailyRatePPM: 0,
	}
	mustRegister(t, e, "p", cfg)
	// 应缴日 10/20/30/40/50，宽限期满次日 13/23/33/43/53 各垫交一次。
	if err := e.Advance("p", 55); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	s := mustQuery(t, e, "p", 55)
	if s.State != StateActive || s.PaidCount != 6 || s.LoanPrincipal != 500 || s.NextDueDay != 60 {
		t.Fatalf("跨周期推进结算错误: %+v", s)
	}
	if s.SettleOps != 10 { // 5 次进入宽限 + 5 次垫交
		t.Fatalf("事件数应为 10: %+v", s)
	}
}

// 复效后重新适用等待天数：等待期内出险不赔付，且与中止不赔付可区分。
func TestWaitingAfterReinstate(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "p", baseConfig())              // 等待 5 天
	if err := e.Reinstate("p", 45, 1000); err != nil { // 中止 40 后于 45 复效
		t.Fatalf("Reinstate: %v", err)
	}
	if r := mustClaim(t, e, "p", 45); r.Verdict != DenyWaiting {
		t.Fatalf("复效当日应在等待期: %+v", r)
	}
	if r := mustClaim(t, e, "p", 49); r.Verdict != DenyWaiting {
		t.Fatalf("等待期末日应不赔付: %+v", r)
	}
	if r := mustClaim(t, e, "p", 50); r.Verdict != PayFull {
		t.Fatalf("等待期满应全额赔付: %+v", r)
	}
	// 可区分：中止不赔付是另一种结论。
	mustRegister(t, e, "q", baseConfig())
	if r := mustClaim(t, e, "q", 40); r.Verdict != DenyLapsed || r.Verdict == DenyWaiting {
		t.Fatalf("中止不赔付应与等待期可区分: %+v", r)
	}
}

// 登记起算的等待期同样生效。
func TestWaitingAtInception(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "p", baseConfig())
	if r := mustClaim(t, e, "p", 4); r.Verdict != DenyWaiting {
		t.Fatalf("登记等待期内应不赔付: %+v", r)
	}
	if r := mustClaim(t, e, "p", 5); r.Verdict != PayFull {
		t.Fatalf("等待期满应全额赔付: %+v", r)
	}
}

// 拒绝次序逐对验证：参数非法 > 保单不存在 > 时钟回退 > 状态不允许 > 补缴不足 > 超额还款。
func TestRejectionOrder(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "p", baseConfig())
	if err := e.Advance("p", 45); err != nil { // 中止于 40，当前时刻 45
		t.Fatalf("Advance: %v", err)
	}
	// 参数非法 > 保单不存在：金额非法且保单不存在，报参数非法。
	if err := e.PayPremium("nope", 45, 0); err != ErrInvalidParam {
		t.Fatalf("应报参数非法, got %v", err)
	}
	// 保单不存在 > 时钟回退：保单不存在且时刻回退，报保单不存在。
	if err := e.PayPremium("nope", 10, 1000); err != ErrPolicyNotFound {
		t.Fatalf("应报保单不存在, got %v", err)
	}
	// 时钟回退 > 状态不允许：中止中还款且时刻回退，报时钟回退。
	if err := e.RepayLoan("p", 30, 100); err != ErrClockRollback {
		t.Fatalf("应报时钟回退, got %v", err)
	}
	// 状态不允许 > 超额还款：宽限中还款且金额超额，报状态不允许。
	mustRegister(t, e, "g", baseConfig())
	if err := e.Advance("g", 35); err != nil { // 宽限中
		t.Fatalf("Advance: %v", err)
	}
	if err := e.RepayLoan("g", 35, 1<<40); err != ErrStateNotAllowed {
		t.Fatalf("应报状态不允许, got %v", err)
	}
	// 状态不允许 > 补缴不足：有效保单复效且金额不足，报状态不允许。
	mustRegister(t, e, "h", baseConfig())
	if err := e.Reinstate("h", 10, 1); err != ErrStateNotAllowed {
		t.Fatalf("应报状态不允许, got %v", err)
	}
	// 补缴不足与超额还款分属复效与还款两个入口，各自独立验证：
	if err := e.Reinstate("p", 45, 999); err != ErrInsufficientPayment {
		t.Fatalf("应报补缴不足, got %v", err)
	}
	cfg := baseConfig()
	cfg.WaitingDays = 0
	cfg.CashValue = []int64{0, 100000, 100000}
	mustRegister(t, e, "r", cfg)
	if err := e.Advance("r", 40); err != nil { // 垫交 1000
		t.Fatalf("Advance: %v", err)
	}
	if err := e.RepayLoan("r", 40, 1001); err != ErrOverpayment {
		t.Fatalf("应报超额还款, got %v", err)
	}
}

// 各类被拒操作均不改变状态、借款账、应缴日与当前时刻。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "p", baseConfig())
	before := mustQuery(t, e, "p", 35) // 宽限中
	rejected := []error{
		e.PayPremium("p", 35, 500),   // 非整数期：参数非法
		e.PayPremium("p", 35, -100),  // 金额非正：参数非法
		e.PayPremium("p", 20, 1000),  // 时钟回退
		e.RepayLoan("p", 35, 100),    // 宽限中还款：状态不允许
		e.Reinstate("p", 35, 1000),   // 宽限中复效：状态不允许
		e.PayPremium("zz", 35, 1000), // 保单不存在
	}
	for i, err := range rejected {
		if err == nil {
			t.Fatalf("第 %d 个操作应被拒绝", i)
		}
		if after := mustQuery(t, e, "p", 35); after != before {
			t.Fatalf("第 %d 个被拒操作留了痕: %+v -> %+v", i, before, after)
		}
	}
}

// 复效须一次性补缴到期保费与已有借款本息（含逐日累计的利息）。
func TestReinstateWithLoanAndInterest(t *testing.T) {
	e := NewEngine()
	cfg := baseConfig()
	cfg.WaitingDays = 0
	cfg.DailyRatePPM = 10 // 每日利息 = ceil(1000*10/10000) = 1 分
	cfg.CashValue = []int64{0, 1500, 1500}
	mustRegister(t, e, "p", cfg)
	// 第 40 天垫交 1000；第 60 天进入宽限，第 70 天净值 1500-(1000+30)=470 < 1000 中止。
	s := mustQuery(t, e, "p", 70)
	if s.State != StateLapsed || s.LoanPrincipal != 1000 || s.LoanInterest != 30 {
		t.Fatalf("带息中止状态错误: %+v", s)
	}
	// 第 75 天复效：需 1000 + 1000 + 35 = 2035。
	if err := e.Reinstate("p", 75, 2034); err != ErrInsufficientPayment {
		t.Fatalf("差 1 分应报补缴不足, got %v", err)
	}
	if err := e.Reinstate("p", 75, 2035); err != nil {
		t.Fatalf("足额复效应成功: %v", err)
	}
	s = mustQuery(t, e, "p", 75)
	if s.State != StateActive || s.PaidCount != 3 || s.LoanPrincipal != 0 || s.LoanInterest != 0 {
		t.Fatalf("复效后状态错误: %+v", s)
	}
}

// 并发调用等价于某个串行顺序：无数据竞争，最终时刻一致。
func TestConcurrentAccess(t *testing.T) {
	e := NewEngine()
	mustRegister(t, e, "a", baseConfig())
	mustRegister(t, e, "b", baseConfig())
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := "a"
			if g%2 == 1 {
				id = "b"
			}
			for i := 1; i <= 100; i++ {
				_ = e.Advance(id, i) // 并发下可能时钟回退，属正常拒绝
				_, _ = e.Query(id, i)
				_, _ = e.Claim(id, i)
			}
		}(g)
	}
	wg.Wait()
	for _, id := range []string{"a", "b"} {
		if s := mustQuery(t, e, id, 100); s.Day != 100 {
			t.Fatalf("并发后时刻应为 100: %+v", s)
		}
	}
}

// 性能证明：推进开销只与区间内事件数成正比，与已经历的缴费期数无关；
// 无事件时的查询零推进开销（O(1)）。
func TestSettleComplexity(t *testing.T) {
	e := NewEngine()
	cfg := Config{
		EffectiveDay: 0, PeriodDays: 1, Premium: 10,
		GraceDays: 1, RevivalDays: 100000, WaitingDays: 0,
		CashValue: []int64{0, 1 << 60}, DailyRatePPM: 0,
	}
	mustRegister(t, e, "p", cfg)
	if err := e.Advance("p", 1000); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	s := mustQuery(t, e, "p", 1000)
	// 1000 次进入宽限 + 999 次垫交，与手算一致。
	if s.SettleOps != 1999 || s.PaidCount != 1000 || s.LoanPrincipal != 9990 {
		t.Fatalf("事件计数错误: %+v", s)
	}
	// 无事件的查询：零推进开销。
	if after := mustQuery(t, e, "p", 1000); after.SettleOps != s.SettleOps {
		t.Fatalf("无事件查询应零开销: %+v", after)
	}
	// 再推进 100 天：恰好 200 个事件（100 次宽限 + 100 次垫交），
	// 与此前已经历的 1999 个事件、1000 期缴费无关。
	if err := e.Advance("p", 1100); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if s := mustQuery(t, e, "p", 1100); s.SettleOps != 2199 {
		t.Fatalf("推进开销应与历史无关: %+v", s)
	}
}
