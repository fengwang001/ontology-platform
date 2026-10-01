// Package retrybudget implements a sliding-window retry budget with dual
// event ledgers, capped deposit counting and escalating retry pricing.
//
// A Budget keeps two append-only event ledgers:
//   - deposit events, recorded by Request, valid at now iff t+Wd > now;
//   - withdrawal events, recorded by allowed TryRetry calls, valid at now
//     iff t+Wc > now, each freezing the cost charged when it was recorded.
//
// Balance(now) = R + D*min(Cm, validDeposits) - sum(frozen costs of valid
// withdrawals). TryRetry charges cost = C*min(Mx, 1+k) where k is the
// number of currently valid withdrawals, and is allowed iff the balance
// covers the cost.
package retrybudget

import (
	"errors"
	"sync"
)

// maxTime is the largest accepted operation timestamp (10^15).
const maxTime = 1_000_000_000_000_000

// Rejection reasons returned by Budget operations. The first matching
// reason is reported, in this order: ErrInvalidTime, ErrClockRegression.
var (
	// ErrInvalidConfig is returned by New when any parameter is out of range.
	ErrInvalidConfig = errors.New("retrybudget: invalid config")
	// ErrInvalidTime is returned when now < 0 or now > 10^15.
	ErrInvalidTime = errors.New("retrybudget: invalid time")
	// ErrClockRegression is returned when now is below the maximum
	// timestamp seen by accepted operations so far.
	ErrClockRegression = errors.New("retrybudget: clock regression")
)

// withdrawal is a recorded retry charge with its frozen cost.
type withdrawal struct {
	time int64
	cost int64
}

// Budget is a sliding-window retry budget. It is safe for concurrent use;
// results are equivalent to some serial order of the operations.
type Budget struct {
	mu sync.Mutex

	wd int64 // deposit window
	wc int64 // withdrawal window
	d  int64 // deposit amount per request
	c  int64 // base retry cost
	r  int64 // floor (guaranteed) balance
	mx int64 // pricing multiplier cap
	cm int64 // deposit count cap

	deposits    []int64      // deposit event times, non-decreasing
	depHead     int          // index of the first live deposit
	withdrawals []withdrawal // withdrawal events, non-decreasing by time
	wdHead      int          // index of the first live withdrawal

	depCount  int64 // number of valid deposits
	wdCount   int64 // number of valid withdrawals
	wdCostSum int64 // sum of frozen costs of valid withdrawals

	maxNow int64 // maximum timestamp accepted so far

	cleaned  int64 // total events removed by amortized cleanup
	examined int64 // total events examined by amortized cleanup
}

// New builds a Budget of the given configuration. Ranges: Wd, Wc in
// [1, 10^9]; D, C in [1, 10^6]; R in [0, 10^9]; Mx in [1, 10]; Cm in
// [1, 10^6]. Any violation rejects the whole configuration with
// ErrInvalidConfig.
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

// checkTime reports the first rejection reason for now, if any.
func (b *Budget) checkTime(now int64) error {
	if now < 0 || now > maxTime {
		return ErrInvalidTime
	}
	if now < b.maxNow {
		return ErrClockRegression
	}
	return nil
}

// expire drops every event whose validity window has closed at now.
// Because accepted timestamps are non-decreasing, events expire in
// registration order and each ledger is scanned from its head only:
// every cleanup examines at most (expired events + 1) entries per
// ledger, so total examined <= cleaned + 2*operations and total
// cleaned <= total registered events.
func (b *Budget) expire(now int64) {
	for b.depHead < len(b.deposits) {
		b.examined++
		if b.deposits[b.depHead]+b.wd > now {
			break
		}
		b.depHead++
		b.depCount--
		b.cleaned++
	}
	for b.wdHead < len(b.withdrawals) {
		b.examined++
		if b.withdrawals[b.wdHead].time+b.wc > now {
			break
		}
		b.wdCostSum -= b.withdrawals[b.wdHead].cost
		b.wdHead++
		b.wdCount--
		b.cleaned++
	}
}

// balanceLocked computes Balance at the current state; b.mu is held.
func (b *Budget) balanceLocked() int64 {
	n := b.depCount
	if n > b.cm {
		n = b.cm
	}
	return b.r + b.d*n - b.wdCostSum
}

// Request records a deposit event at now. The event is valid at time t
// iff its registration time satisfies time+Wd > t.
func (b *Budget) Request(now int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkTime(now); err != nil {
		return err
	}
	b.expire(now)
	b.deposits = append(b.deposits, now)
	b.depCount++
	b.maxNow = now
	return nil
}

// TryRetry attempts to charge a retry at now. The cost is
// C*min(Mx, 1+k) with k the number of withdrawals valid at now. If the
// balance covers the cost, a withdrawal event freezing this cost is
// recorded and true is returned; otherwise nothing is recorded and
// false is returned. A denied retry is still an accepted operation and
// advances the maximum seen timestamp.
func (b *Budget) TryRetry(now int64) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkTime(now); err != nil {
		return false, err
	}
	b.expire(now)
	mult := b.wdCount + 1
	if mult > b.mx {
		mult = b.mx
	}
	cost := b.c * mult
	allowed := b.balanceLocked() >= cost
	if allowed {
		b.withdrawals = append(b.withdrawals, withdrawal{time: now, cost: cost})
		b.wdCount++
		b.wdCostSum += cost
	}
	b.maxNow = now
	return allowed, nil
}

// Balance reports the budget balance at now:
// R + D*min(Cm, validDeposits) - sum of frozen costs of valid
// withdrawals. The result may be negative.
func (b *Budget) Balance(now int64) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkTime(now); err != nil {
		return 0, err
	}
	b.expire(now)
	b.maxNow = now
	return b.balanceLocked(), nil
}

// cleanupStats returns the amortized cleanup counters: total events
// removed and total events examined.
func (b *Budget) cleanupStats() (cleaned, examined int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cleaned, b.examined
}
