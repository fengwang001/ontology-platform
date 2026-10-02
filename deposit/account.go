// Package deposit implements a demand-deposit interest accrual calculator
// (积数计息器). Balances, rates and interest are tracked per day; interest
// is settled over half-open day intervals [prev, d) using the accumulated
// product method with a carried remainder.
package deposit

import (
	"errors"
	"math/big"
	"sort"
	"sync"
)

// D is the interest divisor: annual rate r (in 1/10000 percent units) over
// 360 days, i.e. D = 360 * 10000.
const D = 3600000

const (
	maxStartDay     = 1000000
	maxOpDay        = 10000000
	maxAmount       = 1000000000
	maxRate         = 10000
	maxDelta        = 1000000000
	maxBalance      = 1000000000
	maxSettleDays   = 3660
	maxCorrectedBal = 100000000000
)

// Error values distinguishing every rejection cause.
var (
	ErrInvalidParam      = errors.New("deposit: invalid parameter")
	ErrOutOfOrder        = errors.New("deposit: date before last accepted operation")
	ErrNotSettled        = errors.New("deposit: day not in a settled interval")
	ErrEmptyRange        = errors.New("deposit: settle range is empty")
	ErrInsufficientFunds = errors.New("deposit: insufficient balance")
	ErrBalanceOverflow   = errors.New("deposit: balance exceeds 1e9")
	ErrNegativeBalance   = errors.New("deposit: correction makes a day-end balance negative")
	ErrCorrectedOverflow = errors.New("deposit: correction makes a day-end balance exceed 1e11")
)

// Change describes one settlement record rewritten by Correct.
type Change struct {
	SettleDay    int64
	OldInterest  int64
	NewInterest  int64
	OldRemainder int64
	NewRemainder int64
}

// settleRecord is one settled interval [start, end).
type settleRecord struct {
	start     int64
	end       int64
	interest  int64
	remainder int64 // R after this settlement
}

// credit is interest credited to day-end balances from day `day` on.
type credit struct {
	day    int64
	amount int64
}

// Account is a demand-deposit account. All methods are safe for concurrent
// use; results are equivalent to some serial order.
type Account struct {
	mu      sync.Mutex
	start   int64
	m       int64 // max accepted op date (of the four op kinds)
	prev    int64 // last settle day
	rem     int64 // carried remainder R, 0 <= R < D
	balance int64 // current balance

	flows    map[int64]int64 // net deposit/withdraw/correction per day
	rates    map[int64]int64 // rate set on a day (last one wins)
	rateDays []int64         // sorted keys of rates
	records  []settleRecord  // settled intervals, ordered by end
}

// New creates an account starting at day start with zero balance, zero rate,
// prev == start and R == 0.
func New(start int64) (*Account, error) {
	if start < 0 || start > maxStartDay {
		return nil, ErrInvalidParam
	}
	return &Account{
		start: start,
		m:     start,
		prev:  start,
		flows: make(map[int64]int64),
		rates: make(map[int64]int64),
	}, nil
}

// Deposit adds x to the day-end balance from day d on.
func (a *Account) Deposit(d, x int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d < 0 || d > maxOpDay || x < 1 || x > maxAmount {
		return ErrInvalidParam
	}
	if d < a.m {
		return ErrOutOfOrder
	}
	if a.balance+x > maxBalance {
		return ErrBalanceOverflow
	}
	a.flows[d] += x
	a.balance += x
	a.m = d
	return nil
}

// Withdraw subtracts x from the day-end balance from day d on.
func (a *Account) Withdraw(d, x int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d < 0 || d > maxOpDay || x < 1 || x > maxAmount {
		return ErrInvalidParam
	}
	if d < a.m {
		return ErrOutOfOrder
	}
	if x > a.balance {
		return ErrInsufficientFunds
	}
	a.flows[d] -= x
	a.balance -= x
	a.m = d
	return nil
}

// SetRate sets the annual rate (in 1/10000 percent) from day d on.
func (a *Account) SetRate(d, r int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d < 0 || d > maxOpDay || r < 0 || r > maxRate {
		return ErrInvalidParam
	}
	if d < a.m {
		return ErrOutOfOrder
	}
	if _, ok := a.rates[d]; !ok {
		// d >= a.m and every recorded rate day is <= a.m, so append
		// keeps rateDays sorted.
		a.rateDays = append(a.rateDays, d)
	}
	a.rates[d] = r
	a.m = d
	return nil
}

// Settle settles interest over [prev, d) and credits it from day d on.
func (a *Account) Settle(d int64) (interest, balance, remainder int64, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d < 0 || d > maxOpDay || d-a.prev > maxSettleDays {
		return 0, 0, 0, ErrInvalidParam
	}
	if d < a.m {
		return 0, 0, 0, ErrOutOfOrder
	}
	if d <= a.prev {
		return 0, 0, 0, ErrEmptyRange
	}
	n := a.ledger().sumProducts(a.prev, d)
	n.Add(n, big.NewInt(a.rem))
	q, r := new(big.Int).QuoRem(n, bigD, new(big.Int))
	interest = q.Int64()
	a.rem = r.Int64()
	a.balance += interest
	a.records = append(a.records, settleRecord{
		start:     a.prev,
		end:       d,
		interest:  interest,
		remainder: a.rem,
	})
	a.prev = d
	a.m = d
	return interest, a.balance, a.rem, nil
}

// Correct applies delta to every day-end balance from day d on and
// recomputes the settlement chain.
func (a *Account) Correct(d, delta int64) (balance, remainder int64, changes []Change, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if d < 0 || d > maxOpDay || d < a.start || delta == 0 || delta > maxDelta || delta < -maxDelta {
		return 0, 0, nil, ErrInvalidParam
	}
	if d >= a.prev {
		return 0, 0, nil, ErrNotSettled
	}

	// Tentative flows with the correction applied.
	flows := make(map[int64]int64, len(a.flows)+1)
	for k, v := range a.flows {
		flows[k] = v
	}
	flows[d] += delta

	// Records whose settle day is not after d are unaffected.
	idx := sort.Search(len(a.records), func(i int) bool { return a.records[i].end > d })

	newRecords := make([]settleRecord, len(a.records))
	copy(newRecords, a.records)
	changes = []Change{}

	credits := make([]credit, 0, len(a.records))
	for i := 0; i < idx; i++ {
		credits = append(credits, credit{day: a.records[i].end, amount: a.records[i].interest})
	}
	rin := int64(0)
	if idx > 0 {
		rin = a.records[idx-1].remainder
	}
	for j := idx; j < len(a.records); j++ {
		rec := a.records[j]
		n := newLedger(flows, a.rates, credits).sumProducts(rec.start, rec.end)
		n.Add(n, big.NewInt(rin))
		q, r := new(big.Int).QuoRem(n, bigD, new(big.Int))
		ni, nr := q.Int64(), r.Int64()
		if ni != rec.interest || nr != rec.remainder {
			changes = append(changes, Change{
				SettleDay:    rec.end,
				OldInterest:  rec.interest,
				NewInterest:  ni,
				OldRemainder: rec.remainder,
				NewRemainder: nr,
			})
		}
		newRecords[j].interest = ni
		newRecords[j].remainder = nr
		credits = append(credits, credit{day: rec.end, amount: ni})
		rin = nr
	}

	// Validate every day-end balance in [d, m] before committing.
	final := newLedger(flows, a.rates, credits)
	negative, overflow := false, false
	for k := d; k <= a.m; k++ {
		b := final.balanceAt(k)
		if b < 0 {
			negative = true
		}
		if b > maxCorrectedBal {
			overflow = true
		}
	}
	if negative {
		return 0, 0, nil, ErrNegativeBalance
	}
	if overflow {
		return 0, 0, nil, ErrCorrectedOverflow
	}

	var interestDelta int64
	for j := idx; j < len(newRecords); j++ {
		interestDelta += newRecords[j].interest - a.records[j].interest
	}
	a.flows = flows
	a.records = newRecords
	a.balance += delta + interestDelta
	if len(newRecords) > 0 {
		a.rem = newRecords[len(newRecords)-1].remainder
	}
	return a.balance, a.rem, changes, nil
}

// ledger builds a ledger view of the current state.
func (a *Account) ledger() *ledger {
	credits := make([]credit, len(a.records))
	for i, rec := range a.records {
		credits[i] = credit{day: rec.end, amount: rec.interest}
	}
	return newLedger(a.flows, a.rates, credits)
}

// Balance returns the current balance.
func (a *Account) Balance() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.balance
}

// Remainder returns the current carried remainder R.
func (a *Account) Remainder() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rem
}
