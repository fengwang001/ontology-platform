package retrybudget

import "errors"

var (
	// ErrInvalidConfig means one or more constructor parameters are outside range.
	ErrInvalidConfig = errors.New("invalid retry budget configuration")
	// ErrInvalidTime means now is outside [0, 10^15].
	ErrInvalidTime = errors.New("invalid operation time")
	// ErrClockBacktrack means now is earlier than the maximum accepted time.
	ErrClockBacktrack = errors.New("clock moved backwards")
)

type ledgerEvent struct {
	at   int64
	cost int64
}

// RetryBudget is a concurrency-safe sliding-window budget for retries.
type RetryBudget struct {
	mu             chan struct{}
	depositWindow  int64
	consumeWindow  int64
	depositAmount  int64
	baseCost       int64
	reserve        int64
	maxMultiplier  int64
	depositCap     int64
	latestNow      int64
	deposits       []ledgerEvent
	consumptions   []ledgerEvent
	depositStart   int
	consumeStart   int
	validDeposits  int64
	frozenCost     int64
	cleanedCount   int64
	inspectedCount int64
}

// NewRetryBudget validates configuration and creates an empty retry budget.
func NewRetryBudget(depositWindow, consumeWindow, depositAmount, baseCost, reserve, maxMultiplier, depositCap int64) (*RetryBudget, error) {
	if depositWindow < 1 || depositWindow > 1_000_000_000 ||
		consumeWindow < 1 || consumeWindow > 1_000_000_000 ||
		depositAmount < 1 || depositAmount > 1_000_000 ||
		baseCost < 1 || baseCost > 1_000_000 ||
		reserve < 0 || reserve > 1_000_000_000 ||
		maxMultiplier < 1 || maxMultiplier > 10 ||
		depositCap < 1 || depositCap > 1_000_000 {
		return nil, ErrInvalidConfig
	}

	return &RetryBudget{
		mu:            make(chan struct{}, 1),
		depositWindow: depositWindow,
		consumeWindow: consumeWindow,
		depositAmount: depositAmount,
		baseCost:      baseCost,
		reserve:       reserve,
		maxMultiplier: maxMultiplier,
		depositCap:    depositCap,
	}, nil
}

// Request records one deposit event at now.
func (b *RetryBudget) Request(now int64) error {
	b.mu <- struct{}{}
	defer func() { <-b.mu }()

	if err := b.checkTime(now); err != nil {
		return err
	}

	b.pruneDeposits(now)
	b.pruneConsumptions(now)
	b.deposits = append(b.deposits, ledgerEvent{at: now})
	if b.validDeposits < b.depositCap {
		b.validDeposits++
	}
	b.latestNow = now
	return nil
}

// TryRetry determines whether a retry at now can consume budget.
func (b *RetryBudget) TryRetry(now int64) (bool, error) {
	b.mu <- struct{}{}
	defer func() { <-b.mu }()

	if err := b.checkTime(now); err != nil {
		return false, err
	}

	b.pruneConsumptions(now)
	multiplier := int64(1) + int64(len(b.consumptions)-b.consumeStart)
	if multiplier > b.maxMultiplier {
		multiplier = b.maxMultiplier
	}
	cost := b.baseCost * multiplier

	b.pruneDeposits(now)
	allowed := b.balanceAfterPrune() >= cost
	if allowed {
		b.consumptions = append(b.consumptions, ledgerEvent{at: now, cost: cost})
		b.frozenCost += cost
	}
	b.latestNow = now
	return allowed, nil
}

// Balance returns the available budget at now; it may be negative.
func (b *RetryBudget) Balance(now int64) (int64, error) {
	b.mu <- struct{}{}
	defer func() { <-b.mu }()

	if err := b.checkTime(now); err != nil {
		return 0, err
	}

	b.pruneDeposits(now)
	b.pruneConsumptions(now)
	b.latestNow = now
	return b.balanceAfterPrune(), nil
}

func (b *RetryBudget) checkTime(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	if now < b.latestNow {
		return ErrClockBacktrack
	}
	return nil
}

func (b *RetryBudget) pruneDeposits(now int64) {
	first := b.depositStart
	for first < len(b.deposits) {
		b.inspectedCount++
		if b.deposits[first].at+b.depositWindow > now {
			break
		}
		first++
	}

	removed := first - b.depositStart
	if removed == 0 {
		return
	}

	b.cleanedCount += int64(removed)
	b.depositStart = first
	b.validDeposits = int64(len(b.deposits) - b.depositStart)
	if b.validDeposits > b.depositCap {
		b.validDeposits = b.depositCap
	}

	if b.depositStart*2 >= len(b.deposits) {
		active := copy(b.deposits, b.deposits[b.depositStart:])
		b.deposits = b.deposits[:active]
		b.depositStart = 0
	}
}

func (b *RetryBudget) pruneConsumptions(now int64) {
	first := b.consumeStart
	for first < len(b.consumptions) {
		b.inspectedCount++
		if b.consumptions[first].at+b.consumeWindow > now {
			break
		}
		first++
	}

	removed := first - b.consumeStart
	if removed == 0 {
		return
	}

	for _, event := range b.consumptions[b.consumeStart:first] {
		b.frozenCost -= event.cost
	}
	b.cleanedCount += int64(removed)
	b.consumeStart = first

	if b.consumeStart*2 >= len(b.consumptions) {
		active := copy(b.consumptions, b.consumptions[b.consumeStart:])
		b.consumptions = b.consumptions[:active]
		b.consumeStart = 0
	}
}

func (b *RetryBudget) balanceAfterPrune() int64 {
	return b.reserve + b.depositAmount*b.validDeposits - b.frozenCost
}
