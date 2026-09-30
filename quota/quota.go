package quota

// Package quota implements a period-quota ledger with time-proportional
// proration of usage records across periods and capped leftover rollover.
//
// All time values are plain int64 coordinates in a caller-chosen unit;
// the ledger only cares about their differences.

import (
	"errors"
	"io"
	"log"
	"sort"
	"sync"
)

// Distinguishable rejection reasons.
var (
	// ErrInvalidBaseQuota is returned when Q is not positive.
	ErrInvalidBaseQuota = errors.New("quota: base quota Q must be positive")
	// ErrInvalidPeriodLength is returned when P is not positive.
	ErrInvalidPeriodLength = errors.New("quota: period length P must be positive")
	// ErrNegativeCarryCap is returned when M is negative.
	ErrNegativeCarryCap = errors.New("quota: carryover cap M must not be negative")

	// ErrStartBeforeT0 is returned when a record starts before t0.
	ErrStartBeforeT0 = errors.New("quota: record start s must not be before t0")
	// ErrInvalidInterval is returned when a record has e <= s.
	ErrInvalidInterval = errors.New("quota: record interval [s,e) must satisfy e > s")
	// ErrNegativeAmount is returned when amount is negative.
	ErrNegativeAmount = errors.New("quota: record amount must not be negative")
	// ErrDuplicateID is returned when a record id already exists.
	ErrDuplicateID = errors.New("quota: duplicate record id")

	// ErrNegativePeriod is returned when a queried period index is negative.
	ErrNegativePeriod = errors.New("quota: period index must not be negative")
)

// Record is one usage record over the half-open interval [Start, End).
type Record struct {
	ID     string
	Start  int64
	End    int64
	Amount int64
}

// Report is the result of querying a single period.
type Report struct {
	Period  int64 // period index k
	Usage   int64 // usage allocated into the period
	Quota   int64 // effective quota Q + carry-in
	CarryIn int64 // unused quota carried in from the previous period
	Overage int64 // max(0, usage - effective quota)
}

// Ledger stores usage records and answers period queries.
// A ledger is safe for concurrent use; queries are computed from the
// complete current record set and do not depend on arrival order.
type Ledger struct {
	mu      sync.RWMutex
	t0      int64
	period  int64
	quota   int64
	capM    int64
	records map[string]Record
	logger  *log.Logger
}

// New creates a ledger starting at t0 with period length P, per-period base
// quota Q and carryover cap M.
func New(t0, p, q, m int64) (*Ledger, error) {
	if q <= 0 {
		return nil, ErrInvalidBaseQuota
	}
	if p <= 0 {
		return nil, ErrInvalidPeriodLength
	}
	if m < 0 {
		return nil, ErrNegativeCarryCap
	}
	return &Ledger{
		t0:      t0,
		period:  p,
		quota:   q,
		capM:    m,
		records: make(map[string]Record),
		logger:  log.Default(),
	}, nil
}

// periodStart returns the left endpoint of period k.
func (l *Ledger) periodStart(k int64) int64 {
	return l.t0 + k*l.period
}

// periodIndex returns the period index containing time t (t >= t0).
func (l *Ledger) periodIndex(t int64) int64 {
	return (t - l.t0) / l.period
}

// allocate adds r's prorated shares into usage[k] for every touched period
// with k <= uptoK. It returns the sum of the shares it wrote.
//
// Each touched period receives floor(amount * overlap / length); the unallocated
// remainder (amount minus the sum of floor shares) is added to the last period
// the interval touches, guaranteeing the shares sum to exactly amount.
func (l *Ledger) allocate(usage []int64, r Record, uptoK int64) int64 {
	length := r.End - r.Start
	first := l.periodIndex(r.Start)
	// [s,e) half-open: an endpoint exactly on a boundary does not touch the
	// next period, hence use e-1 to locate the last touched period.
	last := l.periodIndex(r.End - 1)
	stop := last
	if stop > uptoK {
		stop = uptoK
	}

	written := int64(0)
	for k := first; k <= stop; k++ {
		left := r.Start
		if ps := l.periodStart(k); ps > left {
			left = ps
		}
		right := r.End
		if pe := l.periodStart(k + 1); pe < right {
			right = pe
		}
		overlap := right - left
		share := r.Amount * overlap / length
		usage[k] += share
		written += share
	}
	if last <= uptoK {
		usage[last] += r.Amount - written
		written = r.Amount
	}
	return written
}

// usageThrough computes the total allocated usage for periods 0..uptoK from
// the given records (expected in deterministic order).
func (l *Ledger) usageThrough(records []Record, uptoK int64) []int64 {
	usage := make([]int64, uptoK+1)
	for _, r := range records {
		if l.periodIndex(r.Start) > uptoK {
			continue
		}
		l.allocate(usage, r, uptoK)
	}
	return usage
}

// sortedRecords returns all records ordered by ID for deterministic replay.
func (l *Ledger) sortedRecords() []Record {
	out := make([]Record, 0, len(l.records))
	for _, r := range l.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Add validates and stores a usage record. The ledger is unchanged when the
// record is rejected.
func (l *Ledger) Add(id string, s, e, amount int64) error {
	// Validate before taking the write lock. Reasons follow the mandated
	// precedence: start-before-t0, invalid interval, negative amount,
	// duplicate id — only the first applicable error is reported.
	switch {
	case s < l.t0:
		l.logger.Printf("ADD reject id=%q s=%d e=%d amount=%d reason=%q",
			id, s, e, amount, ErrStartBeforeT0.Error())
		return ErrStartBeforeT0
	case e <= s:
		l.logger.Printf("ADD reject id=%q s=%d e=%d amount=%d reason=%q",
			id, s, e, amount, ErrInvalidInterval.Error())
		return ErrInvalidInterval
	case amount < 0:
		l.logger.Printf("ADD reject id=%q s=%d e=%d amount=%d reason=%q",
			id, s, e, amount, ErrNegativeAmount.Error())
		return ErrNegativeAmount
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.records[id]; exists {
		l.logger.Printf("ADD reject id=%q s=%d e=%d amount=%d reason=%q",
			id, s, e, amount, ErrDuplicateID.Error())
		return ErrDuplicateID
	}
	l.records[id] = Record{ID: id, Start: s, End: e, Amount: amount}
	l.logger.Printf("ADD accept id=%q s=%d e=%d amount=%d records=%d",
		id, s, e, amount, len(l.records))
	return nil
}

// Query returns the usage, effective quota, carry-in and overage of period k,
// recomputed from every stored record.
func (l *Ledger) Query(k int64) (Report, error) {
	if k < 0 {
		l.logger.Printf("QUERY reject k=%d reason=%q", k, ErrNegativePeriod.Error())
		return Report{}, ErrNegativePeriod
	}

	l.mu.RLock()
	records := l.sortedRecords()
	l.mu.RUnlock()

	usage := l.usageThrough(records, k)

	// Carry-in for period 0 is fixed at 0; afterwards carry(k) =
	// min(M, max(0, effectiveQuota(k-1) - usage(k-1))).
	carry := int64(0)
	for j := int64(0); j < k; j++ {
		effective := l.quota + carry
		left := effective - usage[j]
		if left < 0 {
			left = 0
		}
		if left > l.capM {
			left = l.capM
		}
		carry = left
	}

	effective := l.quota + carry
	overage := usage[k] - effective
	if overage < 0 {
		overage = 0
	}
	rep := Report{
		Period:  k,
		Usage:   usage[k],
		Quota:   effective,
		CarryIn: carry,
		Overage: overage,
	}
	l.logger.Printf("QUERY k=%d records=%d => usage=%d quota=%d carry=%d overage=%d",
		k, len(records), rep.Usage, rep.Quota, rep.CarryIn, rep.Overage)
	return rep, nil
}

// SetLogger replaces the logger used for input/output/decision logging.
// A nil logger disables logging.
func (l *Ledger) SetLogger(logger *log.Logger) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	l.logger = logger
}
