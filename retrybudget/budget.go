// Package retrybudget implements a sliding-window retry budget with two
// ledgers (deposits and withdrawals), capped deposit counting and
// escalating retry pricing.
//
// Semantics:
//   - Request(now) records a deposit event at time now. A deposit recorded
//     at t is valid at now iff t+Wd > now.
//   - TryRetry(now) counts the currently valid withdrawal events k and
//     prices this retry at cost = C*min(Mx, 1+k). If Balance(now) >= cost a
//     withdrawal event is recorded with the cost frozen at record time
//     (never repriced later) and the retry is allowed; otherwise nothing is
//     recorded and the retry is rejected. A withdrawal recorded at t is
//     valid at now iff t+Wc > now.
//   - Balance(now) = R + D*min(Cm, validDeposits) - sum(frozen costs of
//     valid withdrawals). It may be negative.
//
// All methods are safe for concurrent use; the result is equivalent to some
// serial order. Rejected operations (invalid time, clock regression) do not
// mutate the ledgers or the maximum observed time.
package retrybudget

import (
	"errors"
	"sync"
)

var (
	// ErrInvalidConfig is returned by New when any parameter is out of range.
	ErrInvalidConfig = errors.New("retrybudget: invalid config")
	// ErrInvalidTime is returned when now < 0 or now > 1e15.
	ErrInvalidTime = errors.New("retrybudget: invalid time")
	// ErrClockRegression is returned when now is smaller than the maximum
	// now seen by any accepted operation.
	ErrClockRegression = errors.New("retrybudget: clock regression")
)

// maxTime is the largest legal timestamp (1e15).
const maxTime = int64(1_000_000_000_000_000)

// withdrawal is a recorded retry event with its cost frozen at record time.
type withdrawal struct {
	t    int64
	cost int64
}

// Budget is a sliding-window retry budget. The zero value is not usable;
// construct it with New.
type Budget struct {
	mu sync.Mutex

	wd int64 // deposit window
	wc int64 // withdrawal window
	d  int64 // deposit amount per request
	c  int64 // base retry cost
	r  int64 // floor (reserve) amount
	mx int64 // pricing multiplier cap
	cm int64 // deposit count cap

	// Event ledgers. Because accepted operations have non-decreasing
	// timestamps, events are appended in non-decreasing time order and
	// expire from the front, so each ledger behaves as a queue.
	deposits    []int64
	depHead     int
	withdrawals []withdrawal
	wdHead      int

	// Incrementally maintained aggregates over the valid (unexpired)
	// events, so Balance and TryRetry never rescan the ledgers.
	validDeposits    int64
	validWithdrawals int64
	withdrawCostSum  int64

	maxNow int64

	// Amortized-cleanup bookkeeping (unexported): total events removed by
	// eviction and total events examined while evicting.
	cleanedEvents  int64
	examinedEvents int64
}

// New builds a Budget. Legal ranges: Wd, Wc in [1, 1e9]; D, C in [1, 1e6];
// R in [0, 1e9]; Mx in [1, 10]; Cm in [1, 1e6]. Any violation rejects the
// whole configuration with ErrInvalidConfig.
func New(wd, wc, d, c, r, mx, cm int64) (*Budget, error) {
	if wd < 1 || wd > 1_000_000_000 ||
		wc < 1 || wc > 1_000_000_000 ||
		d < 1 || d > 1_000_000 ||
		c < 1 || c > 1_000_000 ||
		r < 0 || r > 1_000_000_000 ||
		mx < 1 || mx > 10 ||
		cm < 1 || cm > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	return &Budget{wd: wd, wc: wc, d: d, c: c, r: r, mx: mx, cm: cm}, nil
}

// checkTime reports the first rejection reason, in order: invalid time,
// then clock regression.
func (b *Budget) checkTime(now int64) error {
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	if now < b.maxNow {
		return ErrClockRegression
	}
	return nil
}

// evict removes events that have expired at time now from the front of both
// ledgers, maintaining the incremental aggregates. Each event is examined
// and removed at most once, so cleanup is amortized O(1) per operation.
func (b *Budget) evict(now int64) {
	for b.depHead < len(b.deposits) {
		b.examinedEvents++
		if b.deposits[b.depHead]+b.wd > now {
			break
		}
		b.depHead++
		b.validDeposits--
		b.cleanedEvents++
	}
	for b.wdHead < len(b.withdrawals) {
		b.examinedEvents++
		if b.withdrawals[b.wdHead].t+b.wc > now {
			break
		}
		b.withdrawCostSum -= b.withdrawals[b.wdHead].cost
		b.validWithdrawals--
		b.wdHead++
		b.cleanedEvents++
	}
	b.compact()
}

// compact releases fully consumed ledger prefixes so memory stays bounded.
func (b *Budget) compact() {
	if b.depHead > 0 && b.depHead == len(b.deposits) {
		b.deposits = nil
		b.depHead = 0
	} else if b.depHead >= 64 && b.depHead*2 >= len(b.deposits) {
		b.deposits = append([]int64(nil), b.deposits[b.depHead:]...)
		b.depHead = 0
	}
	if b.wdHead > 0 && b.wdHead == len(b.withdrawals) {
		b.withdrawals = nil
		b.wdHead = 0
	} else if b.wdHead >= 64 && b.wdHead*2 >= len(b.withdrawals) {
		b.withdrawals = append([]withdrawal(nil), b.withdrawals[b.wdHead:]...)
		b.wdHead = 0
	}
}

// balanceLocked computes Balance at the current (already evicted) state.
func (b *Budget) balanceLocked() int64 {
	dep := b.validDeposits
	if dep > b.cm {
		dep = b.cm
	}
	return b.r + b.d*dep - b.withdrawCostSum
}

// Request records a deposit event at time now.
func (b *Budget) Request(now int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkTime(now); err != nil {
		return err
	}
	b.evict(now)
	b.deposits = append(b.deposits, now)
	b.validDeposits++
	b.maxNow = now
	return nil
}

// TryRetry attempts to withdraw budget for a retry at time now. It reports
// whether the retry is allowed. A rejected retry (insufficient balance) is
// still an accepted operation: it records nothing but advances maxNow.
func (b *Budget) TryRetry(now int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkTime(now); err != nil {
		return false, err
	}
	b.evict(now)
	k := b.validWithdrawals
	mult := k + 1
	if mult > b.mx {
		mult = b.mx
	}
	cost := b.c * mult
	allowed := b.balanceLocked() >= cost
	if allowed {
		b.withdrawals = append(b.withdrawals, withdrawal{t: now, cost: cost})
		b.validWithdrawals++
		b.withdrawCostSum += cost
	}
	b.maxNow = now
	return allowed, nil
}

// Balance returns the budget balance at time now.
func (b *Budget) Balance(now int64) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkTime(now); err != nil {
		return 0, err
	}
	b.evict(now)
	b.maxNow = now
	return b.balanceLocked(), nil
}
