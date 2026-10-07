package policy

// 本文件实现一个独立的朴素模型：严格按规则逐日推进（每天先累计当日利息，
// 再结算当日事件），用于与事件跳跃式引擎做随机差分对照。

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
)

type naivePolicy struct {
	cfg          Config
	now          int64
	state        State
	effectiveDay int64
	paid         int64
	principal    int64
	interest     int64
	loanCount    int64
	lapseDay     int64
	lapseOwed    int64
	waitingUntil int64
}

func newNaive(cfg Config) *naivePolicy {
	return &naivePolicy{
		cfg:          cfg,
		now:          cfg.EffectiveDay,
		state:        StateActive,
		effectiveDay: cfg.EffectiveDay,
		paid:         1,
		waitingUntil: cfg.EffectiveDay + cfg.WaitingDays,
	}
}

func (n *naivePolicy) nextDue() int64 {
	return n.effectiveDay + n.paid*n.cfg.PeriodDays
}

func (n *naivePolicy) cashValue() int64 {
	idx := n.paid
	if idx >= int64(len(n.cfg.CashValues)) {
		idx = int64(len(n.cfg.CashValues)) - 1
	}
	return n.cfg.CashValues[idx]
}

// step 逐日推进一天：先按当日开始时的本金累计一天利息，再结算当日事件。
func (n *naivePolicy) step() {
	d := n.now + 1
	n.interest += ceilDiv(n.principal*n.cfg.LoanRatePerMyriad, 10000)
	switch n.state {
	case StateActive, StateGrace:
		if d == n.nextDue() {
			n.state = StateGrace
		}
		if d == n.nextDue()+n.cfg.GraceDays {
			if n.cashValue()-(n.principal+n.interest) >= n.cfg.PremiumCents {
				n.principal += n.cfg.PremiumCents
				n.loanCount++
				n.paid++
				n.state = StateActive
				if d >= n.nextDue() {
					n.state = StateGrace
				}
			} else {
				n.state = StateLapsed
				n.lapseDay = d
				n.lapseOwed = (d-n.nextDue())/n.cfg.PeriodDays + 1
			}
		}
	case StateLapsed:
		if d == n.lapseDay+n.cfg.ReinstateDays {
			n.state = StateTerminated
		}
	}
	n.now = d
}

func (n *naivePolicy) advance(day int64) {
	for n.now < day {
		n.step()
	}
}

func (n *naivePolicy) owed() int64 {
	switch n.state {
	case StateActive, StateGrace:
		nd := n.nextDue()
		if n.now < nd {
			return 0
		}
		return ((n.now-nd)/n.cfg.PeriodDays + 1) * n.cfg.PremiumCents
	case StateLapsed:
		return n.lapseOwed * n.cfg.PremiumCents
	}
	return 0
}

func (n *naivePolicy) snapshot() Snapshot {
	return Snapshot{
		Day:           n.now,
		State:         n.state,
		PaidPeriods:   n.paid,
		NextDueDay:    n.nextDue(),
		OwedCents:     n.owed(),
		LoanPrincipal: n.principal,
		LoanInterest:  n.interest,
		LoanCount:     n.loanCount,
		WaitingUntil:  n.waitingUntil,
	}
}

func (n *naivePolicy) adjudicate() ClaimResult {
	switch n.state {
	case StateLapsed:
		return ClaimResult{Reason: ReasonLapsed}
	case StateTerminated:
		return ClaimResult{Reason: ReasonTerminated}
	}
	if n.now < n.waitingUntil {
		return ClaimResult{Reason: ReasonWaiting}
	}
	if n.state == StateGrace {
		amount := n.cfg.SumAssuredCents - n.owed()
		if amount < 0 {
			amount = 0
		}
		return ClaimResult{Pay: amount > 0, AmountCents: amount, Reason: ReasonGraceDeducted}
	}
	return ClaimResult{Pay: true, AmountCents: n.cfg.SumAssuredCents, Reason: ReasonFull}
}

// Query 推进到 day 并返回快照。
func (n *naivePolicy) Query(day int64) (Snapshot, error) {
	if day < 0 {
		return Snapshot{}, ErrInvalidParam
	}
	if day < n.now {
		return Snapshot{}, ErrClockBackward
	}
	n.advance(day)
	return n.snapshot(), nil
}

// Claim 推进到 day 并做出险判定。
func (n *naivePolicy) Claim(day int64) (ClaimResult, error) {
	if day < 0 {
		return ClaimResult{}, ErrInvalidParam
	}
	if day < n.now {
		return ClaimResult{}, ErrClockBackward
	}
	n.advance(day)
	return n.adjudicate(), nil
}

// PayPremium 主动缴费；被拒时不留任何痕迹（在副本上推进）。
func (n *naivePolicy) PayPremium(day, amount int64) error {
	if day < 0 || amount <= 0 {
		return ErrInvalidParam
	}
	if amount%n.cfg.PremiumCents != 0 {
		return ErrInvalidParam
	}
	if day < n.now {
		return ErrClockBackward
	}
	cp := *n
	cp.advance(day)
	if cp.state != StateActive && cp.state != StateGrace {
		return ErrStateNotAllowed
	}
	cp.paid += amount / n.cfg.PremiumCents
	if cp.state == StateGrace && cp.nextDue() > cp.now {
		cp.state = StateActive
	}
	*n = cp
	return nil
}

// RepayLoan 主动还款：仅有效状态；先息后本；超额报「超额还款」。
func (n *naivePolicy) RepayLoan(day, amount int64) error {
	if day < 0 || amount <= 0 {
		return ErrInvalidParam
	}
	if day < n.now {
		return ErrClockBackward
	}
	cp := *n
	cp.advance(day)
	if cp.state != StateActive {
		return ErrStateNotAllowed
	}
	if amount > cp.principal+cp.interest {
		return ErrOverpayment
	}
	if amount <= cp.interest {
		cp.interest -= amount
	} else {
		cp.principal -= amount - cp.interest
		cp.interest = 0
	}
	*n = cp
	return nil
}

// Reinstate 复效：仅中止状态；补缴不足报「补缴不足」且不留痕。
func (n *naivePolicy) Reinstate(day, amount int64) error {
	if day < 0 || amount < 0 {
		return ErrInvalidParam
	}
	if day < n.now {
		return ErrClockBackward
	}
	cp := *n
	cp.advance(day)
	if cp.state != StateLapsed {
		return ErrStateNotAllowed
	}
	required := cp.lapseOwed*n.cfg.PremiumCents + cp.principal + cp.interest
	if amount < required {
		return ErrInsufficientPayment
	}
	cp.principal, cp.interest = 0, 0
	cp.paid += cp.lapseOwed
	cp.effectiveDay = cp.now + cp.cfg.PeriodDays - cp.paid*cp.cfg.PeriodDays
	cp.state = StateActive
	cp.waitingUntil = cp.now + cp.cfg.WaitingDays
	*n = cp
	return nil
}

func randomConfig(rng *rand.Rand) Config {
	cv := make([]int64, 1+rng.IntN(6))
	for i := range cv {
		cv[i] = int64(rng.IntN(20000))
		if i > 0 && cv[i] < cv[i-1] {
			cv[i] = cv[i-1]
		}
	}
	return Config{
		EffectiveDay:      int64(rng.IntN(5)),
		PeriodDays:        int64(1 + rng.IntN(30)),
		PremiumCents:      int64(1+rng.IntN(50)) * 100,
		GraceDays:         int64(1 + rng.IntN(15)),
		ReinstateDays:     int64(1 + rng.IntN(20)),
		WaitingDays:       int64(rng.IntN(10)),
		SumAssuredCents:   int64(1+rng.IntN(2000)) * 100,
		LoanRatePerMyriad: int64(rng.IntN(50)),
		CashValues:        cv,
	}
}

func errName(err error) string {
	if err == nil {
		return "成功"
	}
	return err.Error()
}

// TestDifferentialRandom 对随机生成的缴费、还款、推进与出险序列，
// 逐操作比对事件跳跃式引擎与逐日朴素模型，日志打印输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261007, 1485))
	for trial := 0; trial < 40; trial++ {
		cfg := randomConfig(rng)
		eng := NewEngine()
		id, err := eng.Register(cfg)
		if err != nil {
			t.Fatalf("trial %d: Register: %v", trial, err)
		}
		nv := newNaive(cfg)
		day := cfg.EffectiveDay
		for step := 0; step < 150; step++ {
			day += int64(rng.IntN(25))
			var input, engineOut, naiveOut string
			switch rng.IntN(6) {
			case 0: // 推进 + 查询
				input = fmt.Sprintf("推进并查询(day=%d)", day)
				var es Snapshot
				var ee, ne error
				es, ee = eng.Query(id, day)
				var ns Snapshot
				ns, ne = nv.Query(day)
				engineOut = fmt.Sprintf("%+v err=%v", es, errName(ee))
				naiveOut = fmt.Sprintf("%+v err=%v", ns, errName(ne))
				if !errors.Is(ee, ne) || es != ns {
					t.Fatalf("trial %d step %d %s:\nengine=%s\nnaive =%s", trial, step, input, engineOut, naiveOut)
				}
			case 1: // 缴费：偶发非法金额
				amount := cfg.PremiumCents * int64(1+rng.IntN(3))
				if rng.IntN(5) == 0 {
					amount++
				}
				input = fmt.Sprintf("缴费(day=%d, amount=%d)", day, amount)
				ee := eng.PayPremium(id, day, amount)
				ne := nv.PayPremium(day, amount)
				engineOut, naiveOut = errName(ee), errName(ne)
				if !errors.Is(ee, ne) {
					t.Fatalf("trial %d step %d %s: engine=%s naive=%s", trial, step, input, engineOut, naiveOut)
				}
			case 2: // 还款：偶发超额
				amount := int64(rng.IntN(8000))
				input = fmt.Sprintf("还款(day=%d, amount=%d)", day, amount)
				ee := eng.RepayLoan(id, day, amount)
				ne := nv.RepayLoan(day, amount)
				engineOut, naiveOut = errName(ee), errName(ne)
				if !errors.Is(ee, ne) {
					t.Fatalf("trial %d step %d %s: engine=%s naive=%s", trial, step, input, engineOut, naiveOut)
				}
			case 3: // 复效：金额在应补额附近随机
				amount := int64(rng.IntN(12000))
				input = fmt.Sprintf("复效(day=%d, amount=%d)", day, amount)
				ee := eng.Reinstate(id, day, amount)
				ne := nv.Reinstate(day, amount)
				engineOut, naiveOut = errName(ee), errName(ne)
				if !errors.Is(ee, ne) {
					t.Fatalf("trial %d step %d %s: engine=%s naive=%s", trial, step, input, engineOut, naiveOut)
				}
			case 4: // 出险
				input = fmt.Sprintf("出险(day=%d)", day)
				ec, ee := eng.Claim(id, day)
				nc, ne := nv.Claim(day)
				engineOut = fmt.Sprintf("%+v err=%v", ec, errName(ee))
				naiveOut = fmt.Sprintf("%+v err=%v", nc, errName(ne))
				if !errors.Is(ee, ne) || ec != nc {
					t.Fatalf("trial %d step %d %s:\nengine=%s\nnaive =%s", trial, step, input, engineOut, naiveOut)
				}
			case 5: // 查询
				input = fmt.Sprintf("查询(day=%d)", day)
				es, ee := eng.Query(id, day)
				ns, ne := nv.Query(day)
				engineOut = fmt.Sprintf("%+v err=%v", es, errName(ee))
				naiveOut = fmt.Sprintf("%+v err=%v", ns, errName(ne))
				if !errors.Is(ee, ne) || es != ns {
					t.Fatalf("trial %d step %d %s:\nengine=%s\nnaive =%s", trial, step, input, engineOut, naiveOut)
				}
			}
			// 判定依据：操作后双方状态、欠费与借款账
			es, _ := eng.Query(id, day)
			ns, _ := nv.Query(day)
			if es != ns {
				t.Fatalf("trial %d step %d %s: snapshot mismatch:\nengine=%+v\nnaive =%+v", trial, step, input, es, ns)
			}
			t.Logf("trial=%d step=%d 输入=%s 引擎输出=%s 朴素输出=%s 判定依据=状态%s 欠费%d 借款(本%d 息%d)",
				trial, step, input, engineOut, naiveOut, es.State, es.OwedCents, es.LoanPrincipal, es.LoanInterest)
		}
	}
}
