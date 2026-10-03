package budgetthrottle

import (
	"errors"
	"math/big"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("budgetthrottle: invalid argument")
	ErrClockRolledBack = errors.New("budgetthrottle: clock rolled back")
	ErrBudgetExhausted = errors.New("budgetthrottle: daily budget exhausted")
	ErrAmountTooLarge  = errors.New("budgetthrottle: amount larger than period allowance")
	ErrRateLimited     = errors.New("budgetthrottle: rate limited")
	ErrRefundTooLarge  = errors.New("budgetthrottle: refund larger than spent amount")
)

type Throttler struct {
	budget        int64
	periodLen     int64
	catchUp       int64
	targets       []int64
	targetLookups uint64

	mu          sync.Mutex
	spent       int64
	maxNow      int64
	period      int
	allowance   int64
	periodSpent int64
	started     bool
}

// NewThrottler creates a deterministic daily-budget throttler. The cumulative
// target curve is computed once with arbitrary-precision arithmetic.
func NewThrottler(budget int64, periods int, periodLength int64, weights []int64, catchUpPercent int64) (*Throttler, error) {
	if budget < 1 || budget > 1_000_000_000_000 ||
		periods < 1 || periods > 1440 ||
		periodLength < 1 || periodLength > 1_000_000 ||
		catchUpPercent < 0 || catchUpPercent > 10000 {
		return nil, ErrInvalidArgument
	}
	if len(weights) != periods {
		return nil, ErrInvalidArgument
	}

	targets := make([]int64, periods)
	weightTotal := new(big.Int)
	for _, weight := range weights {
		if weight < 1 || weight > 1_000_000 {
			return nil, ErrInvalidArgument
		}
		weightTotal.Add(weightTotal, big.NewInt(weight))
	}

	cumulativeWeight := new(big.Int)
	bigBudget := big.NewInt(budget)
	for i, weight := range weights {
		cumulativeWeight.Add(cumulativeWeight, big.NewInt(weight))
		value := new(big.Int).Mul(bigBudget, cumulativeWeight)
		value.Quo(value, weightTotal)
		if !value.IsInt64() {
			return nil, ErrInvalidArgument
		}
		targets[i] = value.Int64()
	}

	return &Throttler{
		budget:    budget,
		periodLen: periodLength,
		catchUp:   catchUpPercent,
		targets:   targets,
		period:    -1,
	}, nil
}

// Try accepts a full spend request when it fits both the daily and fixed
// current-period allowance.
func (t *Throttler) Try(amount int64, now int64) error {
	if amount < 1 || amount > 1_000_000_000_000 || !t.validNow(now) {
		return ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if now < t.maxNow {
		return ErrClockRolledBack
	}
	if t.spent+amount < 0 || t.spent+amount > t.budget {
		return ErrBudgetExhausted
	}

	period := t.periodAt(now)
	allowance := t.prospectiveAllowanceLocked(period)
	if amount > allowance {
		return ErrAmountTooLarge
	}
	if t.started && t.period == period && t.periodSpent+amount > allowance {
		return ErrRateLimited
	}

	t.acceptPeriodLocked(period, allowance)
	t.spent += amount
	t.periodSpent += amount
	t.maxNow = now
	return nil
}

// TryUpTo charges the requested amount or the remaining fixed period allowance,
// whichever is smaller. A zero charge is reported as ErrRateLimited.
func (t *Throttler) TryUpTo(amount int64, now int64) (int64, error) {
	if amount < 1 || amount > 1_000_000_000_000 || !t.validNow(now) {
		return 0, ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if now < t.maxNow {
		return 0, ErrClockRolledBack
	}

	period := t.periodAt(now)
	allowance := t.prospectiveAllowanceLocked(period)
	remaining := allowance
	if t.started && t.period == period {
		remaining = allowance - t.periodSpent
	}
	charged := min(amount, remaining)
	if charged < 1 {
		return 0, ErrRateLimited
	}

	t.acceptPeriodLocked(period, allowance)
	t.spent += charged
	t.periodSpent += charged
	t.maxNow = now
	return charged, nil
}

// Refund fixes the current period allowance before reducing spent. Refunding
// spend from an earlier period reduces only spent and enlarges a future deficit.
func (t *Throttler) Refund(amount int64, now int64) error {
	if amount < 1 || amount > 1_000_000_000_000 || !t.validNow(now) {
		return ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if now < t.maxNow {
		return ErrClockRolledBack
	}

	period := t.periodAt(now)
	allowance := t.prospectiveAllowanceLocked(period)
	if amount > t.spent {
		return ErrRefundTooLarge
	}

	t.acceptPeriodLocked(period, allowance)
	t.spent -= amount
	t.periodSpent -= min(t.periodSpent, amount)
	t.maxNow = now
	return nil
}

// Allowance reports current read-only headroom without checking clock rollback
// or recording the observation.
func (t *Throttler) Allowance(now int64) (int64, error) {
	if !t.validNow(now) {
		return 0, ErrInvalidArgument
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	period := t.periodAt(now)
	if t.started && period < t.period {
		return 0, nil
	}
	if t.started && period == t.period {
		return t.allowance - t.periodSpent, nil
	}
	return t.prospectiveAllowanceLocked(period), nil
}

func (t *Throttler) validNow(now int64) bool {
	return now >= 0 && now < int64(len(t.targets))*t.periodLen
}

func (t *Throttler) periodAt(now int64) int {
	return int(now / t.periodLen)
}

func (t *Throttler) targetAt(period int) int64 {
	t.targetLookups++
	return t.targets[period]
}

func (t *Throttler) prospectiveAllowanceLocked(period int) int64 {
	if t.started && t.period == period {
		return t.allowance
	}

	previousTarget := int64(0)
	if period > 0 {
		previousTarget = t.targetAt(period - 1)
	}
	currentTarget := t.targetAt(period)
	periodBudget := currentTarget - previousTarget

	deficit := previousTarget - t.spent
	if deficit < 0 {
		deficit = 0
	}

	periodBudgetBig := new(big.Int).Mul(big.NewInt(periodBudget), big.NewInt(t.catchUp))
	periodBudgetBig.Quo(periodBudgetBig, big.NewInt(100))
	if !periodBudgetBig.IsInt64() {
		return 0
	}
	catchUpCap := periodBudgetBig.Int64()

	catchUp := min(deficit, catchUpCap)
	allowance := periodBudget + catchUp
	if remainingBudget := t.budget - t.spent; allowance > remainingBudget {
		allowance = remainingBudget
	}
	return allowance
}

func (t *Throttler) acceptPeriodLocked(period int, allowance int64) {
	if !t.started || t.period != period {
		t.started = true
		t.period = period
		t.allowance = allowance
		t.periodSpent = 0
	}
}
