// Package checkpoint implements a checkpoint dirty-page flush pacing
// scheduler. It computes, from elapsed time and generated WAL volume, how
// many dirty pages should have been written out cumulatively since the
// checkpoint began.
package checkpoint

import (
	"errors"
	"math/big"
	"sync"
)

var (
	ErrNonPositiveInterval    = errors.New("checkpoint: interval T must be positive")
	ErrNonPositiveBudget      = errors.New("checkpoint: WAL budget W must be positive")
	ErrInvalidTargetFraction  = errors.New("checkpoint: target F must be in [1, 999]")
	ErrNegativeNow            = errors.New("checkpoint: now must not be negative")
	ErrNegativeWalPos         = errors.New("checkpoint: walPos must not be negative")
	ErrNegativeDirty          = errors.New("checkpoint: dirty must not be negative")
	ErrCheckpointInProgress   = errors.New("checkpoint: a checkpoint is already in progress")
	ErrNoCheckpointInProgress = errors.New("checkpoint: no checkpoint is in progress")
	ErrNowRegression          = errors.New("checkpoint: now regresses below a previously accepted value")
	ErrWalPosRegression       = errors.New("checkpoint: walPos regresses below a previously accepted value")
	ErrNonPositiveWrite       = errors.New("checkpoint: written page count k must be positive")
	ErrWriteExceedsRemaining  = errors.New("checkpoint: k exceeds the remaining dirty page count")
)

// Scheduler paces dirty-page writes for checkpoints. All methods are safe
// for concurrent use; the result is equivalent to some serial ordering.
type Scheduler struct {
	mu sync.Mutex

	intervalMs     int64 // T
	walBudget      int64 // W
	targetPermille int64 // F

	active    bool
	startNow  int64
	startWal  int64
	total     int64 // N
	written   int64
	completed int64

	lastNow int64
	lastWal int64
}

// NewScheduler creates a Scheduler. intervalMs is the checkpoint interval T
// in milliseconds, walBudget is the WAL budget W in bytes, and
// targetPermille is the completion target F in permille (1..999).
func NewScheduler(intervalMs, walBudget, targetPermille int64) (*Scheduler, error) {
	if intervalMs <= 0 {
		return nil, ErrNonPositiveInterval
	}
	if walBudget <= 0 {
		return nil, ErrNonPositiveBudget
	}
	if targetPermille < 1 || targetPermille > 999 {
		return nil, ErrInvalidTargetFraction
	}
	return &Scheduler{
		intervalMs:     intervalMs,
		walBudget:      walBudget,
		targetPermille: targetPermille,
	}, nil
}

// Begin starts a checkpoint at (now, walPos) with dirty total pages.
func (s *Scheduler) Begin(now, walPos, dirty int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 {
		return ErrNegativeNow
	}
	if walPos < 0 {
		return ErrNegativeWalPos
	}
	if dirty < 0 {
		return ErrNegativeDirty
	}
	if s.active {
		return ErrCheckpointInProgress
	}
	if now < s.lastNow {
		return ErrNowRegression
	}
	if walPos < s.lastWal {
		return ErrWalPosRegression
	}

	s.lastNow = now
	s.lastWal = walPos
	if dirty == 0 {
		s.completed++
		return nil
	}
	s.active = true
	s.startNow = now
	s.startWal = walPos
	s.total = dirty
	s.written = 0
	return nil
}

// Tick reports the cumulative quota Q and the remaining pages to write.
func (s *Scheduler) Tick(now, walPos int64) (quota, need int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 {
		return 0, 0, ErrNegativeNow
	}
	if walPos < 0 {
		return 0, 0, ErrNegativeWalPos
	}
	if !s.active {
		return 0, 0, ErrNoCheckpointInProgress
	}
	if now < s.lastNow {
		return 0, 0, ErrNowRegression
	}
	if walPos < s.lastWal {
		return 0, 0, ErrWalPosRegression
	}

	s.lastNow = now
	s.lastWal = walPos

	elapsed := now - s.startNow
	walUsed := walPos - s.startWal
	q1 := progressQuota(s.total, elapsed, s.intervalMs, s.targetPermille)
	q2 := progressQuota(s.total, walUsed, s.walBudget, s.targetPermille)
	quota = q1
	if q2 > quota {
		quota = q2
	}
	need = quota - s.written
	if need < 0 {
		need = 0
	}
	return quota, need, nil
}

// progressQuota computes min(n, floor(n*progress*1000/(unit*f))) with exact
// integer arithmetic. n and progress may reach 2^40, so the products are
// evaluated with big.Int to avoid overflow.
func progressQuota(n, progress, unit, f int64) int64 {
	if n <= 0 || progress <= 0 {
		return 0
	}
	num := big.NewInt(n)
	num.Mul(num, big.NewInt(progress))
	num.Mul(num, big.NewInt(1000))
	den := big.NewInt(unit)
	den.Mul(den, big.NewInt(f))
	num.Quo(num, den)
	if !num.IsInt64() || num.Int64() > n {
		return n
	}
	return num.Int64()
}

// Wrote records that k more pages have been written out.
func (s *Scheduler) Wrote(k int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.active {
		return ErrNoCheckpointInProgress
	}
	if k <= 0 {
		return ErrNonPositiveWrite
	}
	if k > s.total-s.written {
		return ErrWriteExceedsRemaining
	}

	s.written += k
	if s.written >= s.total {
		s.active = false
		s.total = 0
		s.written = 0
		s.completed++
	}
	return nil
}

// Status reports whether a checkpoint is in progress, the dirty total N,
// the written count, and the number of completed checkpoints.
func (s *Scheduler) Status() (active bool, total, written, completed int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active, s.total, s.written, s.completed
}
