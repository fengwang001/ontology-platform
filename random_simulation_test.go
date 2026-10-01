package loan

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"
)

type naiveLoan struct {
	principal int64
	bal       int64
	k         int64
	baseR     int64
	a         int64
	rates     map[int64]int64
	h         int
	log       []string
	paidSum   int64
	prepaySum int64
}

type operation struct {
	name   string
	amount int64
	rate   int64
	period int64
	count  int
}

func TestRandomOperationSequences(t *testing.T) {
	t.Log("判定依据: 输入参数范围 -> 结清状态 -> 业务前置条件 -> 候选状态未来 601 期模拟；第 601 期或之后结清为期限超限")
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed_%04d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)^0x9e3779b97f4a7c15))
			principal := int64(rng.IntN(1_000_000) + 1)
			rate := int64(rng.IntN(100_000) + 1)
			payment := int64(rng.IntN(100_000) + 1)

			model := &naiveLoan{
				principal: principal,
				bal:       principal,
				baseR:     rate,
				a:         payment,
				rates:     make(map[int64]int64),
			}
			actual, actualErr := New(principal, rate, payment)
			wantErr := model.new(principal, rate, payment)
			model.logf("New input P=%d r=%d A=%d => error=%v 判定=%s", principal, rate, payment, wantErr, errorReason(wantErr))
			if !sameError(actualErr, wantErr) {
				t.Fatalf("seed=%d New error actual=%v want=%v\n%s", seed, actualErr, wantErr, model.journal())
			}
			if wantErr != nil {
				return
			}

			for step := 0; step < 80; step++ {
				op := randomOperation(rng, model)
				switch op.name {
				case "Pay":
					wantPayment, wantPayErr := model.pay()
					gotPayment, gotPayErr := actual.Pay()
					model.logf("Pay input=<none> => output=%+v error=%v 判定=%s", gotPayment, gotPayErr, errorReason(gotPayErr))
					if !sameError(gotPayErr, wantPayErr) || gotPayment != wantPayment {
						t.Fatalf("seed=%d step=%d Pay actual=(%+v,%v) want=(%+v,%v)\n%s", seed, step, gotPayment, gotPayErr, wantPayment, wantPayErr, model.journal())
					}
				case "Prepay":
					wantPrepayment, wantPrepayErr := model.prepay(op.amount)
					gotPrepayment, gotPrepayErr := actual.Prepay(op.amount)
					model.logf("Prepay input=%d => output=%+v error=%v 判定=%s", op.amount, gotPrepayment, gotPrepayErr, errorReason(gotPrepayErr))
					if !sameError(gotPrepayErr, wantPrepayErr) || gotPrepayment != wantPrepayment {
						t.Fatalf("seed=%d step=%d Prepay(%d) actual=(%+v,%v) want=(%+v,%v)\n%s", seed, step, op.amount, gotPrepayment, gotPrepayErr, wantPrepayment, wantPrepayErr, model.journal())
					}
				case "SetRate":
					wantSetErr := model.setRate(op.rate, op.period)
					gotSetErr := actual.SetRate(op.rate, op.period)
					model.logf("SetRate input r=%d k0=%d => error=%v 判定=%s", op.rate, op.period, gotSetErr, errorReason(gotSetErr))
					if !sameError(gotSetErr, wantSetErr) {
						t.Fatalf("seed=%d step=%d SetRate(%d,%d) actual=%v want=%v\n%s", seed, step, op.rate, op.period, gotSetErr, wantSetErr, model.journal())
					}
				case "Holiday":
					wantHolidayErr := model.holiday(op.count)
					gotHolidayErr := actual.Holiday(op.count)
					model.logf("Holiday input=%d => error=%v 判定=%s", op.count, gotHolidayErr, errorReason(gotHolidayErr))
					if !sameError(gotHolidayErr, wantHolidayErr) {
						t.Fatalf("seed=%d step=%d Holiday(%d) actual=%v want=%v\n%s", seed, step, op.count, gotHolidayErr, wantHolidayErr, model.journal())
					}
				}

				compareState(t, seed, step, actual, model)
				if model.bal == 0 {
					break
				}
			}
		})
	}
}

func randomOperation(rng *rand.Rand, model *naiveLoan) operation {
	switch rng.IntN(4) {
	case 0:
		return operation{name: "Pay"}
	case 1:
		choices := []int64{1, model.a - 1, model.a, model.a + 1, model.bal - 1, model.bal, model.bal + 1, int64(rng.IntN(100_000) + 1)}
		return operation{name: "Prepay", amount: choices[rng.IntN(len(choices))]}
	case 2:
		periodChoices := []int64{1, model.k, model.k + 1, model.k + 2, 1000, 1001}
		return operation{
			name:   "SetRate",
			rate:   int64(rng.IntN(1_000_001) + 1),
			period: periodChoices[rng.IntN(len(periodChoices))],
		}
	default:
		return operation{name: "Holiday", count: int(rng.IntN(14))}
	}
}

func (m *naiveLoan) logf(format string, args ...any) {
	m.log = append(m.log, fmt.Sprintf(format, args...))
}

func (m *naiveLoan) journal() string {
	result := ""
	for _, entry := range m.log {
		result += entry + "\n"
	}
	return result
}

func (m *naiveLoan) new(principal int64, rate int64, payment int64) error {
	m.principal = principal
	if principal < 1 || principal > 1_000_000_000_000 || rate < 1 || rate > 1_000_000 || payment < 1 || payment > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	return m.validate()
}

func (m *naiveLoan) rateAt(period int64) int64 {
	rate := m.baseR
	var from int64
	for period0, rate0 := range m.rates {
		if period0 <= period && period0 > from {
			from = period0
			rate = rate0
		}
	}
	return rate
}

func (m *naiveLoan) interest(period int64) int64 {
	return (m.bal*m.rateAt(period) + 500_000) / 1_000_000
}

func (m *naiveLoan) pay() (Payment, error) {
	if m.bal == 0 {
		return Payment{}, ErrSettled
	}
	period := m.k + 1
	interest := m.interest(period)
	payment := Payment{Period: period, Interest: interest}
	if m.h > 0 {
		payment.Amount = interest
		m.h--
	} else if m.bal+interest <= m.a {
		payment.Amount = m.bal + interest
		payment.Principal = m.bal
		m.bal = 0
	} else {
		if m.a <= interest {
			return Payment{}, ErrNotAmortizable
		}
		payment.Amount = m.a
		payment.Principal = m.a - interest
		m.bal -= payment.Principal
	}
	payment.Balance = m.bal
	m.k++
	m.paidSum += payment.Principal
	return payment, nil
}

func (m *naiveLoan) prepay(amount int64) (Prepayment, error) {
	if amount < 1 || amount > 1_000_000_000_000 {
		return Prepayment{}, ErrInvalidArgument
	}
	if m.bal == 0 {
		return Prepayment{}, ErrSettled
	}
	if amount > m.bal {
		return Prepayment{}, ErrOverpayment
	}
	if amount < m.a && amount != m.bal {
		return Prepayment{}, ErrBelowMinimum
	}
	fee := amount * 300 / 10_000
	if m.k >= 12 {
		fee = amount * 100 / 10_000
	}
	if m.k >= 36 {
		fee = 0
	}
	m.bal -= amount
	m.prepaySum += amount
	return Prepayment{Amount: amount, Fee: fee, Balance: m.bal}, nil
}

func (m *naiveLoan) setRate(rate int64, period int64) error {
	if rate < 1 || rate > 1_000_000 || period < 1 || period > 1000 {
		return ErrInvalidArgument
	}
	if m.bal == 0 {
		return ErrSettled
	}
	if period <= m.k {
		return ErrRatePeriodPassed
	}
	old, existed := m.rates[period]
	m.rates[period] = rate
	if err := m.validate(); err != nil {
		if existed {
			m.rates[period] = old
		} else {
			delete(m.rates, period)
		}
		return err
	}
	return nil
}

func (m *naiveLoan) holiday(count int) error {
	if count < 1 || count > 12 {
		return ErrInvalidArgument
	}
	if m.bal == 0 {
		return ErrSettled
	}
	if m.h > 0 {
		return ErrHolidayActive
	}
	old := m.h
	m.h = count
	if err := m.validate(); err != nil {
		m.h = old
		return err
	}
	return nil
}

func (m *naiveLoan) validate() error {
	copy := m.copy()
	for period := int64(1); period <= simulatedPeriods; period++ {
		if _, err := copy.pay(); err != nil {
			return err
		}
		if copy.bal == 0 {
			if period <= maxPeriods {
				return nil
			}
			return ErrTermExceeded
		}
	}
	return ErrTermExceeded
}

func (m *naiveLoan) remaining() int64 {
	if m.bal == 0 {
		return 0
	}
	copy := m.copy()
	for period := int64(1); period <= maxPeriods; period++ {
		if _, err := copy.pay(); err != nil {
			return maxPeriods
		}
		if copy.bal == 0 {
			return period
		}
	}
	return maxPeriods
}

func (m *naiveLoan) copy() naiveLoan {
	rates := make(map[int64]int64, len(m.rates))
	for period, rate := range m.rates {
		rates[period] = rate
	}
	return naiveLoan{bal: m.bal, k: m.k, baseR: m.baseR, a: m.a, rates: rates, h: m.h}
}

func compareState(t *testing.T, seed int64, step int, actual *Loan, model *naiveLoan) {
	t.Helper()
	if actual.Balance() != model.bal || actual.PaidPeriods() != model.k || actual.h != model.h {
		t.Fatalf("seed=%d step=%d state actual=(bal:%d k:%d h:%d) want=(bal:%d k:%d h:%d)\n%s", seed, step, actual.Balance(), actual.PaidPeriods(), actual.h, model.bal, model.k, model.h, model.journal())
	}
	if len(actual.rateChanges) != len(model.rates) {
		t.Fatalf("seed=%d step=%d rate count actual=%d want=%d", seed, step, len(actual.rateChanges), len(model.rates))
	}
	for period, rate := range model.rates {
		if actual.rateChanges[period] != rate {
			t.Fatalf("seed=%d step=%d rate at %d actual=%d want=%d", seed, step, period, actual.rateChanges[period], rate)
		}
	}
	if actual.Remaining() != model.remaining() {
		t.Fatalf("seed=%d step=%d Remaining actual=%d want=%d", seed, step, actual.Remaining(), model.remaining())
	}
	if model.paidSum+model.prepaySum+model.bal != model.principal {
		t.Fatalf("seed=%d step=%d conservation paid=%d prepay=%d bal=%d principal=%d", seed, step, model.paidSum, model.prepaySum, model.bal, model.principal)
	}
	if model.bal > 0 && model.remaining() > maxPeriods {
		t.Fatalf("seed=%d step=%d remaining exceeds limit", seed, step)
	}
}

func sameError(actual error, want error) bool {
	return errors.Is(actual, want) || (actual == nil && want == nil)
}

func errorReason(err error) string {
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return "参数非法"
	case errors.Is(err, ErrSettled):
		return "已结清"
	case errors.Is(err, ErrOverpayment):
		return "提前还款超额"
	case errors.Is(err, ErrBelowMinimum):
		return "提前还款低于最小额"
	case errors.Is(err, ErrRatePeriodPassed):
		return "利率生效期已过"
	case errors.Is(err, ErrHolidayActive):
		return "已有宽限期"
	case errors.Is(err, ErrNotAmortizable):
		return "未来路径存在 A 不大于利息"
	case errors.Is(err, ErrTermExceeded):
		return "未来路径超过 600 期"
	case err == nil:
		return "通过"
	default:
		return err.Error()
	}
}
