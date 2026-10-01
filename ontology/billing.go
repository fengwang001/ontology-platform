package ontology

import "sync"

var (
	ErrInvalidArgument = invalidArgumentError{}
	ErrDuplicateTrade  = billingError("duplicate trade id")
	ErrCumulativeLimit = billingError("cumulative amount exceeds limit")
	ErrTradeNotFound   = billingError("trade not found")
	ErrTradeCanceled   = billingError("trade is canceled")
	ErrPeriodClosed    = billingError("trade belongs to a closed period")
)

type invalidArgumentError struct{}

func (invalidArgumentError) Error() string { return "invalid argument" }

type billingError string

func (e billingError) Error() string { return string(e) }

// FeeChange describes a fee recomputed after a cancellation.
type FeeChange struct {
	Tid    string
	OldFee int64
	NewFee int64
}

// CumulativeBilling applies a progressive tiered fee to monthly cumulative volume.
type CumulativeBilling struct {
	mu         sync.Mutex
	thresholds []int64
	rates      []int64
	capFee     int64
	carryRate  int64
	period     int64
	accounts   map[string]*billingAccount
	trades     map[string]*tradeRecord
}

type billingAccount struct {
	start  int64
	trades []*tradeRecord
}

type tradeRecord struct {
	tid    string
	acct   string
	amount int64
	period int64
	fee    int64
	active bool
}

func NewCumulativeBilling(thresholds []int64, rates []int64, capFee int64, carryRate int64) (*CumulativeBilling, error) {
	if len(thresholds) < 1 ||
		thresholds[0] < 1 ||
		thresholds[len(thresholds)-1] > maxBillingAmount ||
		len(rates) != len(thresholds)+1 ||
		capFee < 0 || capFee > maxBillingAmount ||
		carryRate < 0 || carryRate > basisPointDenominator {
		return nil, ErrInvalidArgument
	}

	previous := thresholds[0]
	for _, threshold := range thresholds[1:] {
		if threshold <= previous {
			return nil, ErrInvalidArgument
		}
		previous = threshold
	}

	for _, rate := range rates {
		if rate < 0 || rate > basisPointDenominator {
			return nil, ErrInvalidArgument
		}
	}

	thresholdCopy := append([]int64(nil), thresholds...)
	rateCopy := append([]int64(nil), rates...)

	return &CumulativeBilling{
		thresholds: thresholdCopy,
		rates:      rateCopy,
		capFee:     capFee,
		carryRate:  carryRate,
		accounts:   make(map[string]*billingAccount),
		trades:     make(map[string]*tradeRecord),
	}, nil
}

func (b *CumulativeBilling) Trade(acct string, tid string, amount int64) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if acct == "" || tid == "" || amount < 1 || amount > 1_000_000_000 {
		return 0, ErrInvalidArgument
	}
	if _, exists := b.trades[tid]; exists {
		return 0, ErrDuplicateTrade
	}

	account := b.accounts[acct]
	start := int64(0)
	total := int64(0)
	if account == nil {
	} else {
		start = account.start
		total = account.currentTotal(b.period)
	}

	if start+total+amount > maxBillingAmount {
		return 0, ErrCumulativeLimit
	}

	if account == nil {
		account = &billingAccount{}
		b.accounts[acct] = account
	}

	record := &tradeRecord{
		tid:    tid,
		acct:   acct,
		amount: amount,
		period: b.period,
		active: true,
	}
	previousCumulative := start + total
	cumulative := previousCumulative + amount
	record.fee = b.cumulativeFee(cumulative) - b.cumulativeFee(previousCumulative)

	b.trades[tid] = record
	account.trades = append(account.trades, record)
	return record.fee, nil
}

func (b *CumulativeBilling) Cancel(tid string) (int64, []FeeChange, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if tid == "" {
		return 0, nil, ErrInvalidArgument
	}

	record, exists := b.trades[tid]
	if !exists {
		return 0, nil, ErrTradeNotFound
	}
	if !record.active {
		return 0, nil, ErrTradeCanceled
	}
	if record.period != b.period {
		return 0, nil, ErrPeriodClosed
	}

	canceledFee := record.fee
	record.active = false

	account := b.accounts[record.acct]
	changes := b.recompute(account)
	if changes == nil {
		changes = []FeeChange{}
	}
	return canceledFee, changes, nil
}

func (b *CumulativeBilling) NextPeriod() {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, account := range b.accounts {
		total := account.currentTotal(b.period)
		account.start = (account.start + total) * b.carryRate / basisPointDenominator
	}
	b.period++
}

func (b *CumulativeBilling) CurrentPeriod() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.period
}

func (b *CumulativeBilling) AccountStart(acct string) (int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	account, exists := b.accounts[acct]
	if !exists {
		return 0, false
	}
	return account.start, true
}

func (b *CumulativeBilling) recompute(account *billingAccount) []FeeChange {
	cumulative := account.start
	var changes []FeeChange
	for _, record := range account.trades {
		if record.period != b.period || !record.active {
			continue
		}

		previousCumulative := cumulative
		cumulative += record.amount
		newFee := b.cumulativeFee(cumulative) - b.cumulativeFee(previousCumulative)
		if record.fee != newFee {
			changes = append(changes, FeeChange{
				Tid:    record.tid,
				OldFee: record.fee,
				NewFee: newFee,
			})
		}
		record.fee = newFee
	}
	return changes
}

func (a *billingAccount) currentTotal(period int64) int64 {
	var total int64
	for _, record := range a.trades {
		if record.period == period && record.active {
			total += record.amount
		}
	}
	return total
}
