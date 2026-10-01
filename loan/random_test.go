package loan

import (
	"errors"
	"math/rand"
	"testing"
)

type naiveLoan struct {
	principal     int64
	balance       int64
	paidPeriods   int64
	initialRate   int64
	payment       int64
	rates         map[int64]int64
	holidayLeft   int64
	principalPaid int64
	prepaid       int64
}

func TestRandomSequencesMatchNaiveSimulation(t *testing.T) {
	random := rand.New(rand.NewSource(1_138))
	created := 0

	for sequence := 0; created < 2000; sequence++ {
		principal := int64(random.Intn(100_000) + 1)
		payment := int64(random.Intn(int(principal*2)+1) + 1)
		if random.Intn(3) == 0 && payment > 1 {
			payment /= 2
			if payment == 0 {
				payment = 1
			}
		}
		rate := int64(random.Intn(30_000) + 1)

		actual, actualErr := New(principal, rate, payment)
		model, modelErr := newNaive(principal, rate, payment)
		t.Logf("seq=%d input=New(P=%d,r=%d,A=%d) output=err=%v basis=naive:%v",
			sequence, principal, rate, payment, errorName(actualErr), errorName(modelErr))
		if !sameError(actualErr, modelErr) {
			t.Fatalf("New(%d,%d,%d) actual error = %v, naive error = %v", principal, rate, payment, actualErr, modelErr)
		}
		if actualErr != nil {
			continue
		}
		created++

		operationCount := random.Intn(16) + 4
		for operation := 0; operation < operationCount; operation++ {
			if model.balance == 0 {
				break
			}

			switch random.Intn(5) {
			case 0, 1:
				beforeRemaining := actual.Remaining()
				actualPayment, actualErr := actual.Pay()
				modelPayment, modelErr := model.pay()
				t.Logf("seq=%d op=%d input=Pay output=%+v err=%v basis=next=%d bal=%d h=%d naive=%+v/%v",
					created, operation, actualPayment, errorName(actualErr), model.paidPeriods+1, model.balance, model.holidayLeft, modelPayment, modelErr)
				if !sameError(actualErr, modelErr) || actualPayment != modelPayment {
					t.Fatalf("Pay mismatch: actual (%+v,%v), naive (%+v,%v)", actualPayment, actualErr, modelPayment, modelErr)
				}
				if remaining := actual.Remaining(); remaining != model.remaining() || remaining > beforeRemaining {
					t.Fatalf("Remaining after Pay = %d, naive = %d, before = %d", remaining, model.remaining(), beforeRemaining)
				}

			case 2:
				amount := randomPrepayment(random, model)
				beforeRemaining := actual.Remaining()
				actualPrepayment, actualErr := actual.Prepay(amount)
				modelPrepayment, modelErr := model.prepay(amount)
				t.Logf("seq=%d op=%d input=Prepay(x=%d) output=%+v err=%v basis=k=%d bal=%d A=%d naive=%+v/%v",
					created, operation, amount, actualPrepayment, errorName(actualErr), model.paidPeriods, model.balance, model.payment, modelPrepayment, modelErr)
				if !sameError(actualErr, modelErr) || actualPrepayment != modelPrepayment {
					t.Fatalf("Prepay(%d) mismatch: actual (%+v,%v), naive (%+v,%v)", amount, actualPrepayment, actualErr, modelPrepayment, modelErr)
				}
				if actualErr == nil && actual.Remaining() > beforeRemaining {
					t.Fatalf("Remaining increased after Prepay: before=%d after=%d", beforeRemaining, actual.Remaining())
				}

			case 3:
				effectivePeriod := model.paidPeriods + int64(random.Intn(6))
				if random.Intn(10) == 0 {
					effectivePeriod = model.paidPeriods
				}
				newRate := model.rateAt(effectivePeriod) + int64(random.Intn(20_001)) - 10_000
				if newRate <= 0 {
					newRate = 1
				}
				if random.Intn(20) == 0 {
					newRate = int64(random.Intn(1_000_002))
				}

				actualErr := actual.SetRate(newRate, effectivePeriod)
				modelErr := model.setRate(newRate, effectivePeriod)
				t.Logf("seq=%d op=%d input=SetRate(r2=%d,k0=%d) output=err=%v basis=k=%d rates=%v h=%d naive=%v",
					created, operation, newRate, effectivePeriod, errorName(actualErr), model.paidPeriods, model.rates, model.holidayLeft, errorName(modelErr))
				if !sameError(actualErr, modelErr) {
					t.Fatalf("SetRate(%d,%d) actual error = %v, naive error = %v", newRate, effectivePeriod, actualErr, modelErr)
				}

			case 4:
				count := int64(random.Intn(14) + 1)
				if random.Intn(5) != 0 {
					count = int64(random.Intn(12) + 1)
				}
				actualErr := actual.Holiday(count)
				modelErr := model.holiday(count)
				t.Logf("seq=%d op=%d input=Holiday(c=%d) output=err=%v basis=k=%d h=%d bal=%d naive=%v",
					created, operation, count, errorName(actualErr), model.paidPeriods, model.holidayLeft, model.balance, errorName(modelErr))
				if !sameError(actualErr, modelErr) {
					t.Fatalf("Holiday(%d) actual error = %v, naive error = %v", count, actualErr, modelErr)
				}
			}

			if actual.Remaining() != model.remaining() {
				t.Fatalf("Remaining actual = %d, naive = %d", actual.Remaining(), model.remaining())
			}
			if model.balance > 0 && (actual.Remaining() > 600 || model.remaining() > 600) {
				t.Fatalf("Remaining exceeds limit: actual=%d naive=%d", actual.Remaining(), model.remaining())
			}
			assertStateMatchesNaive(t, actual, model)
		}
	}

	if created != 2000 {
		t.Fatalf("created %d successful loans, want 2000", created)
	}
}

func randomPrepayment(random *rand.Rand, model *naiveLoan) int64 {
	switch random.Intn(5) {
	case 0:
		return model.payment - 1
	case 1:
		return model.payment
	case 2:
		return model.balance
	case 3:
		return model.balance + 1
	default:
		span := model.balance - model.payment
		if span <= 0 {
			return model.balance
		}
		return model.payment + random.Int63n(span+1)
	}
}

func sameError(actual error, expected error) bool {
	if actual == nil || expected == nil {
		return actual == expected
	}
	return errors.Is(actual, expected)
}

func errorName(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}
