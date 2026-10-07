package policy

import (
	"math/rand"
	"testing"
)

// naivePolicy 独立朴素模型：逐日推进，每天先累计当日利息，
// 再结算当日全部事件。它与引擎的事件跳跃式推进相互独立，
// 用于对照验证任意操作序列下两者轨迹完全一致。
type naivePolicy struct {
	cfg        Config
	day        int
	state      State
	paid       int
	graceDue   int
	lapseDay   int
	waitingEnd int
	principal  int64
	interest   int64
	frozen     bool // 终止后停止计息
}

func newNaive(cfg Config) *naivePolicy {
	return &naivePolicy{
		cfg:        cfg,
		day:        cfg.EffectiveDay,
		state:      StateActive,
		paid:       1,
		waitingEnd: cfg.EffectiveDay + cfg.WaitingDays - 1,
	}
}

func (n *naivePolicy) nextDue() int {
	return n.cfg.EffectiveDay + n.paid*n.cfg.PeriodDays
}

func (n *naivePolicy) cashValue() int64 {
	cv := n.cfg.CashValue
	if n.paid >= len(cv) {
		return cv[len(cv)-1]
	}
	return cv[n.paid]
}

func (n *naivePolicy) dailyInterest() int64 {
	if n.frozen {
		return 0
	}
	return (n.principal*n.cfg.DailyRatePPM + perMyriad - 1) / perMyriad
}

// settleEvents 结算当前日的全部事件（与引擎规则一致）。
func (n *naivePolicy) settleEvents() {
	for {
		switch n.state {
		case StateActive:
			if n.nextDue() <= n.day {
				n.state = StateGrace
				n.graceDue = n.nextDue()
				continue
			}
		case StateGrace:
			if n.day >= n.graceDue+n.cfg.GraceDays {
				if n.cashValue()-n.principal-n.interest >= n.cfg.Premium {
					n.principal += n.cfg.Premium
					n.paid++
					n.state = StateActive
				} else {
					n.state = StateLapsed
					n.lapseDay = n.day
				}
				continue
			}
		case StateLapsed:
			if n.day >= n.lapseDay+n.cfg.RevivalDays {
				n.state = StateTerminated
				n.frozen = true
				continue
			}
		}
		return
	}
}

// advanceOneDay 推进一天：先计当日利息，再结算当日事件。
func (n *naivePolicy) advanceOneDay() {
	n.interest += n.dailyInterest()
	n.day++
	n.settleEvents()
}

func (n *naivePolicy) settle(day int) {
	for n.day < day {
		n.advanceOneDay()
	}
}

// advance 与引擎 Advance 对齐：回退拒绝，否则逐日推进到 day。
func (n *naivePolicy) advance(day int) error {
	if day < 0 {
		return ErrInvalidParam
	}
	if day < n.day {
		return ErrClockRollback
	}
	n.settle(day)
	return nil
}

func (n *naivePolicy) owed() int64 {
	switch n.state {
	case StateGrace, StateLapsed:
		return n.cfg.Premium + n.principal + n.interest
	case StateActive:
		return n.principal + n.interest
	default:
		return 0
	}
}

func (n *naivePolicy) pay(day int, amount int64) error {
	if day < 0 || amount <= 0 {
		return ErrInvalidParam
	}
	if amount%n.cfg.Premium != 0 {
		return ErrInvalidParam
	}
	if day < n.day {
		return ErrClockRollback
	}
	cp := *n
	cp.settle(day)
	if cp.state != StateActive && cp.state != StateGrace {
		return ErrStateNotAllowed
	}
	cp.paid += int(amount / cp.cfg.Premium)
	cp.state = StateActive
	cp.settleEvents()
	*n = cp
	return nil
}

func (n *naivePolicy) repay(day int, amount int64) error {
	if day < 0 || amount <= 0 {
		return ErrInvalidParam
	}
	if day < n.day {
		return ErrClockRollback
	}
	cp := *n
	cp.settle(day)
	if cp.state != StateActive {
		return ErrStateNotAllowed
	}
	if amount > cp.principal+cp.interest {
		return ErrOverpayment
	}
	if amount <= cp.interest {
		cp.interest -= amount
	} else {
		amount -= cp.interest
		cp.interest = 0
		cp.principal -= amount
	}
	*n = cp
	return nil
}

func (n *naivePolicy) reinstate(day int, amount int64) error {
	if day < 0 || amount <= 0 {
		return ErrInvalidParam
	}
	if day < n.day {
		return ErrClockRollback
	}
	cp := *n
	cp.settle(day)
	if cp.state != StateLapsed {
		return ErrStateNotAllowed
	}
	required := cp.cfg.Premium + cp.principal + cp.interest
	if amount < required {
		return ErrInsufficientPayment
	}
	cp.principal, cp.interest = 0, 0
	cp.paid++
	cp.state = StateActive
	cp.waitingEnd = day + cp.cfg.WaitingDays - 1
	cp.settleEvents()
	*n = cp
	return nil
}

func (n *naivePolicy) claim(day int) (ClaimResult, error) {
	if day < 0 {
		return ClaimResult{}, ErrInvalidParam
	}
	if day < n.day {
		return ClaimResult{}, ErrClockRollback
	}
	n.settle(day)
	res := ClaimResult{}
	switch {
	case n.day <= n.waitingEnd && (n.state == StateActive || n.state == StateGrace):
		res.Verdict = DenyWaiting
	case n.state == StateActive:
		res.Verdict = PayFull
	case n.state == StateGrace:
		res.Verdict = PayReduced
		res.Deduction = n.cfg.Premium
	case n.state == StateLapsed:
		res.Verdict = DenyLapsed
	default:
		res.Verdict = DenyTerminated
	}
	return res, nil
}

// naiveView 与引擎 Snapshot 对照用的朴素视图。
type naiveView struct {
	day       int
	state     State
	paid      int
	principal int64
	interest  int64
	owed      int64
	inWaiting bool
}

func (n *naivePolicy) view() naiveView {
	return naiveView{
		day:       n.day,
		state:     n.state,
		paid:      n.paid,
		principal: n.principal,
		interest:  n.interest,
		owed:      n.owed(),
		inWaiting: n.day <= n.waitingEnd,
	}
}

func randomConfig(rng *rand.Rand) Config {
	cvLen := 1 + rng.Intn(5)
	cv := make([]int64, cvLen)
	for i := range cv {
		var prev int64
		if i > 0 {
			prev = cv[i-1]
		}
		cv[i] = prev + rng.Int63n(2000)
	}
	return Config{
		EffectiveDay: rng.Intn(5),
		PeriodDays:   1 + rng.Intn(30),
		Premium:      1 + rng.Int63n(2000),
		GraceDays:    1 + rng.Intn(15),
		RevivalDays:  1 + rng.Intn(20),
		WaitingDays:  rng.Intn(6),
		CashValue:    cv,
		DailyRatePPM: rng.Int63n(50),
	}
}

// 随机对照测试：随机缴费、还款、推进与出险序列，
// 引擎与朴素模型必须给出完全相同的错误、状态轨迹、借款余额与赔付结论。
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	const trials = 30
	const steps = 300
	for trial := 0; trial < trials; trial++ {
		cfg := randomConfig(rng)
		eng := NewEngine()
		if err := eng.Register("p", cfg); err != nil {
			t.Fatalf("trial %d Register: %v", trial, err)
		}
		nv := newNaive(cfg)
		now := cfg.EffectiveDay
		for step := 0; step < steps; step++ {
			var errEng, errNav error
			var verdictEng, verdictNav ClaimResult
			op := ""
			var amount int64
			now += rng.Intn(25) // now 单调不减，引擎与朴素模型时刻都不超过 now
			day := now
			if rng.Intn(20) == 0 && now > cfg.EffectiveDay {
				day = now - 1 // 回退探针：双方应一致报时钟回退且不留痕
			}
			switch rng.Intn(6) {
			case 0:
				op = "advance"
				errEng = eng.Advance("p", day)
				errNav = nv.advance(day)
			case 1:
				op = "pay"
				if rng.Intn(2) == 0 {
					amount = cfg.Premium * int64(1+rng.Intn(3))
				} else {
					amount = rng.Int63n(4*cfg.Premium + 2)
				}
				errEng = eng.PayPremium("p", day, amount)
				errNav = nv.pay(day, amount)
			case 2:
				op = "repay"
				amount = rng.Int63n(3*cfg.Premium + 500)
				errEng = eng.RepayLoan("p", day, amount)
				errNav = nv.repay(day, amount)
			case 3:
				op = "reinstate"
				amount = rng.Int63n(5*cfg.Premium + 500)
				errEng = eng.Reinstate("p", day, amount)
				errNav = nv.reinstate(day, amount)
			case 4:
				op = "claim"
				verdictEng, errEng = eng.Claim("p", day)
				verdictNav, errNav = nv.claim(day)
			default:
				op = "query"
				_, errEng = eng.Query("p", day)
				errNav = nv.advance(day)
			}
			if errEng != errNav {
				t.Fatalf("trial %d step %d op=%s day=%d amt=%d: 错误不一致 eng=%v naive=%v",
					trial, step, op, day, amount, errEng, errNav)
			}
			if op == "claim" && verdictEng != verdictNav {
				t.Fatalf("trial %d step %d: 判定不一致 eng=%+v naive=%+v",
					trial, step, verdictEng, verdictNav)
			}
			nv.settle(now) // 对齐到同一时刻比较（被拒操作不推进时钟）
			s, err := eng.Query("p", now)
			if err != nil {
				t.Fatalf("trial %d step %d Query: %v", trial, step, err)
			}
			v := nv.view()
			match := s.Day == v.day && s.State == v.state && s.PaidCount == v.paid &&
				s.LoanPrincipal == v.principal && s.LoanInterest == v.interest &&
				s.OwedAmount == v.owed && s.InWaiting == v.inWaiting
			t.Logf("trial=%d step=%d op=%s day=%d amt=%d err=%v | state=%s paid=%d principal=%d interest=%d owed=%d waiting=%v verdict=%s | 依据: 状态=%s 等待期=%v",
				trial, step, op, day, amount, errEng, s.State, s.PaidCount,
				s.LoanPrincipal, s.LoanInterest, s.OwedAmount, s.InWaiting,
				verdictEng.Verdict, s.State, s.InWaiting)
			if !match {
				t.Fatalf("trial %d step %d op=%s: 轨迹不一致\neng=%+v\nnaive=%+v",
					trial, step, op, s, v)
			}
		}
	}
}
