package card_test

import (
	"math/big"

	"ontology/card"
)

// This file holds an independent, deliberately naive model of the same
// business rules, used as the oracle for randomized differential tests.
// It differs from the production implementation structurally:
//
//   - interest is accrued day by day in an explicit loop (O(days));
//   - repayment windows are recomputed by scanning the whole operation
//     log instead of being attributed incrementally;
//   - the most recent unpaid-minimum bill is found by scanning all
//     bills instead of using a stack;
//   - the full state is recomputed from scratch after every operation.
//
// It is slow but obviously close to the specification text.
type nkind int

const (
	nCharge nkind = iota
	nBill
	nRepay
)

type nop struct {
	kind   nkind
	day    int64
	cat    card.Category
	amount int64
}

type nbill struct {
	billDay, dueDay int64
	total, minDue   int64
	interest        [3]int64
	lateFee         int64
	paidInWindow    int64
	minRemaining    int64
	opIdx           int // index of the billing op in the log
}

type nstate struct {
	bal   [3]int64
	over  int64
	bills []nbill
	reps  []card.Repayment
}

const nUnit = 10000 * 365

// replayNaive recomputes the full account state from the accepted
// operation log. startDay is the account creation day.
func replayNaive(p card.Params, startDay int64, ops []nop) nstate {
	var st nstate
	var base [3]big.Int
	var carry [3]int64
	lastDay := startDay
	prevFullyPaid := true // first period is treated as fully paid

	// rateDesc orders categories by rate descending, ties by category.
	rateDesc := []int{0, 1, 2}
	for i := 0; i < 3; i++ {
		for j := i + 1; j < 3; j++ {
			ri, rj := rateDesc[i], rateDesc[j]
			if p.Rates[rj] > p.Rates[ri] || (p.Rates[rj] == p.Rates[ri] && rj < ri) {
				rateDesc[i], rateDesc[j] = rj, ri
			}
		}
	}

	for idx, op := range ops {
		// Naive accrual: iterate every single day, adding that day's
		// start-of-day balance times the rate.
		for d := lastDay + 1; d <= op.day; d++ {
			for c := 0; c < 3; c++ {
				base[c].Add(&base[c], big.NewInt(st.bal[c]*p.Rates[c]))
			}
		}
		lastDay = op.day

		switch op.kind {
		case nCharge:
			amt := op.amount
			if st.over > 0 {
				use := min(st.over, amt)
				st.over -= use
				amt -= use
			}
			st.bal[op.cat] += amt

		case nRepay:
			// Attribute to the newest bill whose window is still open.
			if n := len(st.bills); n > 0 && op.day <= st.bills[n-1].dueDay {
				st.bills[n-1].paidInWindow += op.amount
			}

			var alloc [3]int64
			remaining := op.amount
			var toMin int64

			// Find the most recent bill with unsatisfied minimum by
			// scanning all bills backwards.
			for i := len(st.bills) - 1; i >= 0; i-- {
				if st.bills[i].minRemaining <= 0 {
					continue
				}
				portion := min(remaining, st.bills[i].minRemaining)
				st.bills[i].minRemaining -= portion
				toMin = portion
				left := portion
				for c := 0; c < 3 && left > 0; c++ {
					take := min(st.bal[c], left)
					st.bal[c] -= take
					alloc[c] += take
					left -= take
					remaining -= take
				}
				break
			}
			for _, c := range rateDesc {
				if remaining == 0 {
					break
				}
				take := min(st.bal[c], remaining)
				st.bal[c] -= take
				alloc[c] += take
				remaining -= take
			}
			st.over += remaining
			st.reps = append(st.reps, card.Repayment{
				Seq:    len(st.reps),
				Day:    op.day,
				Amount: op.amount,
				Alloc:  alloc,
				ToMin:  toMin,
				Over:   remaining,
			})

		case nBill:
			// Recompute the previous bill's window sum by scanning the
			// whole operation log: repayments after the previous
			// billing op, up to and including its due day.
			var fee int64
			if len(st.bills) > 0 {
				prev := &st.bills[len(st.bills)-1]
				var windowSum int64
				for j := prev.opIdx + 1; j < idx; j++ {
					if ops[j].kind == nRepay && ops[j].day <= prev.dueDay {
						windowSum += ops[j].amount
					}
				}
				prev.paidInWindow = windowSum
				prevFullyPaid = windowSum >= prev.total
				if windowSum < prev.minDue {
					fee = min(p.LateFeeCap, prev.minDue-windowSum)
				}
			}

			var interest [3]int64
			for c := 0; c < 3; c++ {
				waived := c == int(card.Purchase) && prevFullyPaid
				if waived {
					base[c].SetInt64(0)
					continue
				}
				total := new(big.Int).Add(&base[c], big.NewInt(carry[c]))
				q := new(big.Int).Div(total, big.NewInt(nUnit))
				r := new(big.Int).Mod(total, big.NewInt(nUnit))
				interest[c] = q.Int64()
				carry[c] = r.Int64()
				base[c].SetInt64(0)
			}

			for c := 0; c < 3; c++ {
				st.bal[c] += interest[c]
			}
			st.bal[card.Purchase] += fee

			total := st.bal[0] + st.bal[1] + st.bal[2]
			var minDue int64
			if total > 0 {
				isum := interest[0] + interest[1] + interest[2]
				other := total - isum - fee
				minDue = isum + fee + (other*p.MinPayRatio+9999)/10000
				if minDue < p.MinPayFloor {
					minDue = p.MinPayFloor
				}
				if minDue > total {
					minDue = total
				}
			}

			st.bills = append(st.bills, nbill{
				billDay:      op.day,
				dueDay:       op.day + p.GraceDays,
				total:        total,
				minDue:       minDue,
				interest:     interest,
				lateFee:      fee,
				minRemaining: minDue,
				opIdx:        idx,
			})
		}
	}
	return st
}
