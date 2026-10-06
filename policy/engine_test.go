package policy

import (
	"errors"
	"sync"
	"testing"
)

func baseCfg() Config {
	return Config{
		EffectiveDay:   10,
		AnnualPremium:  1000,
		BaseSumAssured: 10000,
		MinSumAssured:  1000,
		CoolingDays:    10,
		PolicyFee:      50,
		PayPeriods:     10,
		RatioTable:     []int64{10, 30, 50},
	}
}

func mustErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want %v, got %v", target, err)
	}
}

func (e *Engine) AdvanceToMust(t *testing.T, day int64) error {
	t.Helper()
	_, err := e.AdvanceTime(day)
	return err
}

// 保单年度右端取等落入下一档；比例表未给出年度沿用末档。
func TestPolicyYearAndRatio(t *testing.T) {
	if y := policyYear(10, 10); y != 1 {
		t.Fatalf("day0 year=%d", y)
	}
	if y := policyYear(374, 10); y != 1 {
		t.Fatalf("day364 year=%d", y)
	}
	if y := policyYear(375, 10); y != 2 {
		t.Fatalf("day365 year=%d", y)
	}
	table := []int64{10, 30}
	if ratioOf(table, 1) != 10 || ratioOf(table, 2) != 30 || ratioOf(table, 5) != 30 {
		t.Fatalf("ratio fallback wrong")
	}
}

// 犹豫期末日与次日退保。
func TestCoolingEndBoundary(t *testing.T) {
	e := New()
	e.Register("p", baseCfg())
	e.AdvanceToMust(t, 10)
	if err := e.PayPremium("p", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.AdvanceToMust(t, 19); err != nil {
		t.Fatal(err)
	}
	r, err := e.FullSurrender("p")
	if err != nil {
		t.Fatal(err)
	}
	if r.Payout != 1000-50 {
		t.Fatalf("cooling last day payout=%d", r.Payout)
	}

	e2 := New()
	e2.Register("p", baseCfg())
	e2.AdvanceToMust(t, 10)
	e2.PayPremium("p", 1)
	e2.AdvanceToMust(t, 20)
	r2, err := e2.FullSurrender("p")
	if err != nil {
		t.Fatal(err)
	}
	if r2.Payout != 100 {
		t.Fatalf("post-cooling payout=%d", r2.Payout)
	}
}

// 现金价值减借款后为零。
func TestCashValueLoanFloor(t *testing.T) {
	e := New()
	e.Register("p", baseCfg())
	e.AdvanceToMust(t, 10)
	e.PayPremium("p", 1)
	e.AdvanceToMust(t, 400)
	if cv, _ := e.CashValue("p", 400, 500); cv != 0 {
		t.Fatalf("cv after loan=%d", cv)
	}
	if cv, _ := e.CashValue("p", 400, 100); cv != 200 {
		t.Fatalf("cv=%d", cv)
	}
}

// 生效日恰等于当前时刻的预约立即生效。
func TestScheduleEffectiveNow(t *testing.T) {
	e := New()
	e.Register("p", baseCfg())
	e.AdvanceToMust(t, 100)
	err := e.Schedule(EndRequest{
		PolicyID: "p", EndID: "e1", Kind: KindBeneficiaryChange,
		ApplyDay: 100, EffectiveDay: 100, NewBeneficiary: "Bob",
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := e.GetEndorsement("p", "e1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != StatusEffective {
		t.Fatalf("status=%d", info.Status)
	}
	v, _ := e.GetPolicy("p")
	if v.Beneficiary != "Bob" {
		t.Fatalf("beneficiary=%s", v.Beneficiary)
	}
}

// 同日多条预约按申请次序生效。
func TestSameDayOrder(t *testing.T) {
	e := New()
	e.Register("p", baseCfg())
	e.AdvanceToMust(t, 0)
	for i, name := range []string{"a", "b", "c"} {
		err := e.Schedule(EndRequest{
			PolicyID: "p", EndID: name, Kind: KindBeneficiaryChange,
			ApplyDay: int64(i), EffectiveDay: 50, NewBeneficiary: name,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.AdvanceTime(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || res[0].EndID != "a" || res[1].EndID != "b" || res[2].EndID != "c" {
		t.Fatalf("order=%v", res)
	}
	v, _ := e.GetPolicy("p")
	if v.Beneficiary != "c" {
		t.Fatalf("final beneficiary=%s", v.Beneficiary)
	}
}

// 保额增加补缴向上取整；减少退还向下取整。
func TestAmountChangeRounding(t *testing.T) {
	e := New()
	e.Register("p", baseCfg())
	e.AdvanceToMust(t, 10)
	e.PayPremium("p", 1)
	e.Schedule(EndRequest{
		PolicyID: "p", EndID: "up", Kind: KindAmountChange,
		ApplyDay: 10, EffectiveDay: 10, NewSumAssured: 10003,
	})
	info, _ := e.GetEndorsement("p", "up")
	if info.Status != StatusAwaitingPay || info.Surcharge != 1 {
		t.Fatalf("up status=%d surcharge=%d", info.Status, info.Surcharge)
	}
	if _, err := e.FullSurrender("p"); err != nil {
		mustErrIs(t, err, ErrAwaitingPay)
	}
	if _, err := e.PaySurcharge("p", "up", 1); err != nil {
		t.Fatal(err)
	}
	v, _ := e.GetPolicy("p")
	if v.AnnualPremium != 1001 || v.SumAssured != 10003 {
		t.Fatalf("up applied premium=%d sa=%d", v.AnnualPremium, v.SumAssured)
	}

	// 减少：保额 10000->9000 => 新保费900，生效日 day11 剩余364天，
	// 退还 floor(100*364/365)=99。
	e2 := New()
	e2.Register("p", baseCfg())
	e2.AdvanceToMust(t, 10)
	e2.PayPremium("p", 1)
	e2.Schedule(EndRequest{
		PolicyID: "p", EndID: "down", Kind: KindAmountChange,
		ApplyDay: 10, EffectiveDay: 11, NewSumAssured: 9000,
	})
	if err := e2.AdvanceToMust(t, 11); err != nil {
		t.Fatal(err)
	}
	info2, _ := e2.GetEndorsement("p", "down")
	if info2.Status != StatusEffective || info2.Refund != 99 {
		t.Fatalf("down status=%d refund=%d", info2.Status, info2.Refund)
	}
	v2, _ := e2.GetPolicy("p")
	if v2.AnnualPremium != 900 {
		t.Fatalf("down premium=%d", v2.AnnualPremium)
	}
}

// 待补缴停留且其后批改照常生效。
func TestAwaitingPayDoesNotBlockLater(t *testing.T) {
	e := New()
	e.Register("p", baseCfg())
	e.AdvanceToMust(t, 10)
	e.PayPremium("p", 1)
	e.Schedule(EndRequest{
		PolicyID: "p", EndID: "a", Kind: KindAmountChange,
		ApplyDay: 10, EffectiveDay: 20, NewSumAssured: 20000,
	})
	e.Schedule(EndRequest{
		PolicyID: "p", EndID: "b", Kind: KindBeneficiaryChange,
		ApplyDay: 10, EffectiveDay: 30, NewBeneficiary: "Zoe",
	})
	res, err := e.AdvanceTime(40)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].EndID != "a" || res[0].Status != StatusAwaitingPay ||
		res[1].EndID != "b" || res[1].Status != StatusEffective {
		t.Fatalf("res=%+v", res)
	}
}

// 存在预约批改时整单退保被拒，撤销后可退保。
func TestSurrenderBlockedByScheduled(t *testing.T) {
	e := New()
	e.Register("p", baseCfg())
	e.AdvanceToMust(t, 10)
	e.Schedule(EndRequest{
		PolicyID: "p", EndID: "a", Kind: KindBeneficiaryChange,
		ApplyDay: 10, EffectiveDay: 500, NewBeneficiary: "x",
	})
	_, err := e.FullSurrender("p")
	mustErrIs(t, err, ErrPendingEnds)
	if err := e.Cancel("p", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.FullSurrender("p"); err != nil {
		t.Fatalf("surrender after cancel: %v", err)
	}
}

// 部分退保恰等于最低保额，再降被拒。
func TestPartialSurrenderAtMin(t *testing.T) {
	e := New()
	cfg := baseCfg()
	e.Register("p", cfg)
	e.AdvanceToMust(t, 10)
	e.PayPremium("p", 1)
	e.AdvanceToMust(t, 375)
	reduce := cfg.BaseSumAssured - cfg.MinSumAssured
	r, err := e.PartialSurrender("p", reduce)
	if err != nil {
		t.Fatal(err)
	}
	if r.Payout != 270 || r.SumAssured != 1000 {
		t.Fatalf("r=%+v", r)
	}
	if r.AnnualPremium != 100 {
		t.Fatalf("premium=%d", r.AnnualPremium)
	}
	_, err = e.PartialSurrender("p", 1)
	mustErrIs(t, err, ErrBelowMinSA)
}

func TestPartialInCoolingRejected(t *testing.T) {
	e := New()
	e.Register("p", baseCfg())
	e.AdvanceToMust(t, 10)
	_, err := e.PartialSurrender("p", 100)
	mustErrIs(t, err, ErrInvalid)
}

// 缴费限制与重复缴费。
func TestPaymentRules(t *testing.T) {
	e := New()
	e.Register("p", baseCfg())
	e.AdvanceToMust(t, 10)
	mustErrIs(t, e.PayPremium("p", 3), ErrInvalid)
	if err := e.PayPremium("p", 1); err != nil {
		t.Fatal(err)
	}
	mustErrIs(t, e.PayPremium("p", 1), ErrAlreadyPaid)
	if err := e.PayPremium("p", 2); err != nil {
		t.Fatal(err)
	}
	v, _ := e.GetPolicy("p")
	if v.PaidTotal != 2000 {
		t.Fatalf("paid=%d", v.PaidTotal)
	}
}

// 时钟回退、追溯批改、已生效撤销、终态操作。
func TestClockRetroAndTerminal(t *testing.T) {
	e := New()
	e.Register("p", baseCfg())
	e.AdvanceToMust(t, 100)
	_, err := e.AdvanceTime(99)
	mustErrIs(t, err, ErrClockBack)
	err = e.Schedule(EndRequest{
		PolicyID: "p", EndID: "e", Kind: KindBeneficiaryChange,
		ApplyDay: 100, EffectiveDay: 99, NewBeneficiary: "n",
	})
	mustErrIs(t, err, ErrRetroactive)
	err = e.Schedule(EndRequest{
		PolicyID: "p", EndID: "e", Kind: KindBeneficiaryChange,
		ApplyDay: 100, EffectiveDay: 120, NewBeneficiary: "n",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.AdvanceToMust(t, 120)
	mustErrIs(t, e.Cancel("p", "e"), ErrEndEffective)
	_, err = e.FullSurrender("p")
	if err != nil {
		t.Fatal(err)
	}
	mustErrIs(t, e.PayPremium("p", 1), ErrTerminated)
	_, err = e.FullSurrender("p")
	mustErrIs(t, err, ErrTerminated)
}

// 被拒操作不留痕：预约被追溯拒绝后，保单无该批改且可正常退保。
func TestRejectedOperationLeavesNoTrace(t *testing.T) {
	e := New()
	e.Register("p", baseCfg())
	e.AdvanceToMust(t, 100)
	err := e.Schedule(EndRequest{
		PolicyID: "p", EndID: "ghost", Kind: KindBeneficiaryChange,
		ApplyDay: 100, EffectiveDay: 50, NewBeneficiary: "n",
	})
	mustErrIs(t, err, ErrRetroactive)
	_, err = e.GetEndorsement("p", "ghost")
	mustErrIs(t, err, ErrEndMissing)
	if _, err := e.FullSurrender("p"); err != nil {
		t.Fatalf("policy should be clean: %v", err)
	}
}

// 并发撤销与生效同一预约批改：结果必与两种串行顺序之一一致。
func TestConcurrentCancelVsAdvance(t *testing.T) {
	for iter := 0; iter < 200; iter++ {
		e := New()
		e.Register("p", baseCfg())
		e.AdvanceToMust(t, 0)
		e.Schedule(EndRequest{
			PolicyID: "p", EndID: "e", Kind: KindBeneficiaryChange,
			ApplyDay: 0, EffectiveDay: 10, NewBeneficiary: "n",
		})
		var wg sync.WaitGroup
		wg.Add(2)
		var cancelErr, advanceErr error
		go func() { defer wg.Done(); cancelErr = e.Cancel("p", "e") }()
		go func() {
			defer wg.Done()
			_, advanceErr = e.AdvanceTime(10)
		}()
		wg.Wait()
		if advanceErr != nil {
			t.Fatal(advanceErr)
		}
		info, _ := e.GetEndorsement("p", "e")
		switch info.Status {
		case StatusEffective:
			// 先生效：撤销必须报已生效，不得退款。
			mustErrIs(t, cancelErr, ErrEndEffective)
		case StatusCancelled:
			if cancelErr != nil {
				t.Fatalf("cancel should succeed: %v", cancelErr)
			}
		default:
			t.Fatalf("illegal status=%d", info.Status)
		}
		v, _ := e.GetPolicy("p")
		if v.Beneficiary == "n" && info.Status == StatusCancelled {
			t.Fatal("既撤销又生效")
		}
	}
}

// 拒绝次序：参数非法 > 保单不存在 > 时钟回退 > 已终态 > 批改不存在 >
// 批改重复 > 已生效 > 追溯批改 > 待补缴 > 存在未生效批改 > 已缴 > 低于最低保额。
// 本用例逐对构造“两个原因同时成立”的输入，断言只报次序最前者。
func TestRejectionOrderPairs(t *testing.T) {
	// 参数非法 压过 保单不存在（空ID + 不存在）
	if _, err := New().GetPolicy(""); !errIs(err, ErrInvalid) {
		t.Fatalf("invalid vs missing: %v", err)
	}
	// 参数非法 压过 保单不存在（Register）
	bad := baseCfg()
	bad.AnnualPremium = 0
	if err := New().Register("nope", bad); !errIs(err, ErrInvalid) {
		t.Fatalf("invalid reg: %v", err)
	}

	e := New()
	e.Register("p", baseCfg())

	// 时钟回退 压过 已终态：先终止保单再回退。
	e.AdvanceToMust(t, 10)
	if _, err := e.FullSurrender("p"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AdvanceTime(9); !errIs(err, ErrClockBack) {
		t.Fatalf("clockback vs terminated: %v", err)
	}

	// 已终态 压过 批改不存在
	if err := e.Cancel("p", "missing"); !errIs(err, ErrTerminated) {
		t.Fatalf("terminated vs endmissing: %v", err)
	}
	// 已终态 压过 批改重复/追溯：用已终止保单再预约
	err := e.Schedule(EndRequest{
		PolicyID: "p", EndID: "x", Kind: KindBeneficiaryChange,
		ApplyDay: 10, EffectiveDay: 9, NewBeneficiary: "b",
	})
	if !errIs(err, ErrTerminated) {
		t.Fatalf("terminated vs retro/dup: %v", err)
	}

	// 批改不存在 压过 已生效/追溯：对不存在批改调用撤销
	e2 := New()
	e2.Register("p", baseCfg())
	e2.AdvanceToMust(t, 10)
	if err := e2.Cancel("p", "missing"); !errIs(err, ErrEndMissing) {
		t.Fatalf("endmissing: %v", err)
	}

	// 已生效 压过 追溯：撤销已生效批改（假设撤销会做追溯校验）
	e2.Schedule(EndRequest{
		PolicyID: "p", EndID: "d", Kind: KindBeneficiaryChange,
		ApplyDay: 10, EffectiveDay: 10, NewBeneficiary: "b",
	})
	if err := e2.Cancel("p", "d"); !errIs(err, ErrEndEffective) {
		t.Fatalf("effective: %v", err)
	}

	// 待补缴 压过 存在未生效批改：一张待补缴 + 一张预约时退保
	e3 := New()
	e3.Register("p", baseCfg())
	e3.AdvanceToMust(t, 10)
	e3.PayPremium("p", 1)
	e3.Schedule(EndRequest{
		PolicyID: "p", EndID: "await", Kind: KindAmountChange,
		ApplyDay: 10, EffectiveDay: 10, NewSumAssured: 20000,
	})
	e3.Schedule(EndRequest{
		PolicyID: "p", EndID: "later", Kind: KindBeneficiaryChange,
		ApplyDay: 10, EffectiveDay: 500, NewBeneficiary: "b",
	})
	if _, err := e3.FullSurrender("p"); !errIs(err, ErrAwaitingPay) {
		t.Fatalf("awaiting vs pending: %v", err)
	}

	// 存在未生效批改 压过 已缴/低于最低保额
	e4 := New()
	e4.Register("p", baseCfg())
	e4.AdvanceToMust(t, 10)
	e4.PayPremium("p", 1)
	e4.Schedule(EndRequest{
		PolicyID: "p", EndID: "later", Kind: KindBeneficiaryChange,
		ApplyDay: 10, EffectiveDay: 500, NewBeneficiary: "b",
	})
	// 整单退保只报存在未生效批改（即使已缴费也不报已缴）
	if _, err := e4.FullSurrender("p"); !errIs(err, ErrPendingEnds) {
		t.Fatalf("pending: %v", err)
	}

	// 已缴 压过 低于最低保额：部分退保场景无法同时触发两者，
	// 已缴独立验证（重复缴费）。
	if err := e4.PayPremium("p", 1); !errIs(err, ErrAlreadyPaid) {
		t.Fatalf("alreadypaid: %v", err)
	}
	if err := e4.Cancel("p", "later"); err != nil {
		t.Fatal(err)
	}
	e4.AdvanceToMust(t, 375)
	if _, err := e4.PartialSurrender("p", 999999); !errIs(err, ErrBelowMinSA) {
		t.Fatalf("belowmin: %v", err)
	}
}

func errIs(err, target error) bool {
	return errors.Is(err, target)
}
func benchCfg() Config {
	return Config{
		EffectiveDay: 0, AnnualPremium: 1000, BaseSumAssured: 100000,
		MinSumAssured: 100, CoolingDays: 1, PolicyFee: 0, PayPeriods: 100000,
		RatioTable: []int64{100},
	}
}

// buildPayments 构造 n 个已缴年度（时钟严格前进），缴费记录数随 n 增长。
func buildPayments(b *testing.B, n int) *Engine {
	b.Helper()
	e := New()
	if err := e.Register("p", benchCfg()); err != nil {
		b.Fatal(err)
	}
	for y := int64(1); y <= int64(n); y++ {
		if _, err := e.AdvanceTime((y - 1) * 365); err != nil {
			b.Fatal(err)
		}
		if err := e.PayPremium("p", y); err != nil {
			b.Fatal(err)
		}
	}
	return e
}

// buildEnds 构造 n 条已生效批改，再堆入 n 条未来预约（堆规模随 n 增长）。
func buildEnds(b *testing.B, n int) *Engine {
	b.Helper()
	e := New()
	if err := e.Register("p", benchCfg()); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		day := int64(i * 2)
		if _, err := e.AdvanceTime(day); err != nil {
			b.Fatal(err)
		}
		if err := e.Schedule(EndRequest{
			PolicyID: "p", EndID: "past" + itoa(i), Kind: KindBeneficiaryChange,
			ApplyDay: day, EffectiveDay: day, NewBeneficiary: "b",
		}); err != nil {
			b.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		day := int64(1000000 + int64(i)*2)
		if err := e.Schedule(EndRequest{
			PolicyID: "p", EndID: "fut" + itoa(i), Kind: KindBeneficiaryChange,
			ApplyDay: int64(n * 2), EffectiveDay: day, NewBeneficiary: "f",
		}); err != nil {
			b.Fatal(err)
		}
	}
	return e
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// BenchmarkCashValueO1：缴费历史从 100 到 4000，单次现金价值耗时应基本不增长。
func BenchmarkCashValueO1(b *testing.B) {
	for _, n := range []int{100, 1000, 4000} {
		e := buildPayments(b, n)
		day := int64(n) * 365
		b.Run(itoa(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := e.CashValue("p", day, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkNextDueOlogN：堆顶取下一条应生效批改与已生效批改数无关，
// 仅与当前未生效堆规模成 O(log n)。
func BenchmarkNextDueOlogN(b *testing.B) {
	for _, n := range []int{100, 1000, 4000} {
		e := buildEnds(b, n)
		b.Run(itoa(n), func(b *testing.B) {
			p := e.policies["p"]
			for i := 0; i < b.N; i++ {
				_ = p.heap.peek()
			}
		})
	}
}
