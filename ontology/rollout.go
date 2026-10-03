package ontology

import (
	"errors"
	"math/big"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrInvalidState    = errors.New("invalid state")
	ErrClockRolledBack = errors.New("clock rolled back")
)

type State int

const (
	// StateNotStarted is the state before a successful Start.
	StateNotStarted State = iota
	// StateRunning means at least one batch is accepting observations.
	StateRunning
	// StateCompleted means the final batch passed its gate.
	StateCompleted
	// StateAborted means a rollback happened on batch zero.
	StateAborted
	// StateFrozen means the rollback limit was reached after moving to an earlier batch.
	StateFrozen
)

// Reviewer deterministically evaluates canary batch gates and rollouts.
type Reviewer struct {
	mu sync.Mutex

	totalInstances int64
	cumulative     []int64
	soakDuration   int64
	passesRequired int
	tolerance      int
	errorFloor     int64
	minSamples     int64
	failureLimit   int
	silence        int64
	rollbackLimit  int

	state         State
	batch         int
	startTime     int64
	newRequests   int64
	newErrors     int64
	baseRequests  int64
	baseErrors    int64
	passStreak    int
	failures      int
	rollbackCount int
	maxNow        int64
}

// Status is a point-in-time copy of reviewer state and counters.
type Status struct {
	State            State
	Batch            int
	Instances        int64
	StartTime        int64
	NewRequests      int64
	NewErrors        int64
	BaselineRequests int64
	BaselineErrors   int64
	PassStreak       int
	Failures         int
	RollbackCount    int
}

// ObserveResult describes an accepted observation's resulting review outcome.
type ObserveResult struct {
	Accepted bool
	Reason   error
	Outcome  string
}

const (
	OutcomeSoaking            = "soaking"
	OutcomeInsufficientSample = "insufficient_sample"
	OutcomePassed             = "passed"
	OutcomeFailed             = "failed"
	OutcomeAdvanced           = "advanced"
	OutcomeCompleted          = "completed"
	OutcomeRolledBack         = "rolled_back"
	OutcomeAborted            = "aborted"
	OutcomeFrozen             = "frozen"
)

// NewReviewer validates configuration and builds deduplicated cumulative batches.
func NewReviewer(totalInstances int64, percentages []int, soakDuration int64, passesRequired int, tolerancePercent int, absoluteErrorFloor int64, minSamples int64, failureLimit int, rollbackSilence int64, rollbackLimit int) (*Reviewer, error) {
	if totalInstances < 1 || totalInstances > 1_000_000 {
		return nil, ErrInvalidArgument
	}
	if len(percentages) < 1 || len(percentages) > 8 {
		return nil, ErrInvalidArgument
	}
	for index, percentage := range percentages {
		if percentage < 1 || percentage > 100 {
			return nil, ErrInvalidArgument
		}
		if index > 0 && percentage <= percentages[index-1] {
			return nil, ErrInvalidArgument
		}
	}
	if percentages[len(percentages)-1] != 100 {
		return nil, ErrInvalidArgument
	}
	if soakDuration < 0 || soakDuration > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if passesRequired < 1 || passesRequired > 100 {
		return nil, ErrInvalidArgument
	}
	if tolerancePercent < 0 || tolerancePercent > 1000 {
		return nil, ErrInvalidArgument
	}
	if absoluteErrorFloor < 0 || absoluteErrorFloor > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if minSamples < 0 || minSamples > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if failureLimit < 1 || failureLimit > 100 {
		return nil, ErrInvalidArgument
	}
	if rollbackSilence < 0 || rollbackSilence > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	if rollbackLimit < 1 || rollbackLimit > 100 {
		return nil, ErrInvalidArgument
	}

	cumulative := make([]int64, 0, len(percentages))
	for _, percentage := range percentages {
		count := ceilDiv(totalInstances*int64(percentage), 100)
		if len(cumulative) == 0 || count != cumulative[len(cumulative)-1] {
			cumulative = append(cumulative, count)
		}
	}

	return &Reviewer{
		totalInstances: totalInstances,
		cumulative:     cumulative,
		soakDuration:   soakDuration,
		passesRequired: passesRequired,
		tolerance:      tolerancePercent,
		errorFloor:     absoluteErrorFloor,
		minSamples:     minSamples,
		failureLimit:   failureLimit,
		silence:        rollbackSilence,
		rollbackLimit:  rollbackLimit,
	}, nil
}

// Start begins batch zero and records the initial batch start time.
func (r *Reviewer) Start(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateNotStarted {
		return ErrInvalidState
	}
	if now < r.maxNow {
		return ErrClockRolledBack
	}

	r.state = StateRunning
	r.batch = 0
	r.startTime = now
	r.maxNow = now
	r.resetCountersLocked()
	return nil
}

// Observe adds counters and, after soaking, performs one deterministic gate review.
func (r *Reviewer) Observe(now, newRequests, newErrors, baselineRequests, baselineErrors int64) (ObserveResult, error) {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ObserveResult{}, ErrInvalidArgument
	}
	if err := validateIncrement(newRequests, newErrors); err != nil {
		return ObserveResult{}, err
	}
	if err := validateIncrement(baselineRequests, baselineErrors); err != nil {
		return ObserveResult{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateRunning {
		return ObserveResult{}, ErrInvalidState
	}
	if now < r.maxNow {
		return ObserveResult{}, ErrClockRolledBack
	}

	nextNewRequests := r.newRequests + newRequests
	nextNewErrors := r.newErrors + newErrors
	nextBaseRequests := r.baseRequests + baselineRequests
	nextBaseErrors := r.baseErrors + baselineErrors
	if nextNewRequests > 1_000_000_000 || nextNewErrors > 1_000_000_000 ||
		nextBaseRequests > 1_000_000_000 || nextBaseErrors > 1_000_000_000 {
		return ObserveResult{}, ErrInvalidArgument
	}

	r.maxNow = now
	r.newRequests = nextNewRequests
	r.newErrors = nextNewErrors
	r.baseRequests = nextBaseRequests
	r.baseErrors = nextBaseErrors

	if now < r.startTime+r.soakDuration {
		return ObserveResult{Accepted: true, Outcome: OutcomeSoaking}, nil
	}
	if r.newRequests < r.minSamples {
		return ObserveResult{Accepted: true, Outcome: OutcomeInsufficientSample}, nil
	}

	failed := r.newErrors >= r.errorFloor && gateFails(
		r.newErrors,
		r.baseRequests,
		r.newRequests,
		r.baseErrors,
		int64(100+r.tolerance),
	)
	if !failed {
		r.passStreak++
		if r.passStreak < r.passesRequired {
			return ObserveResult{Accepted: true, Outcome: OutcomePassed}, nil
		}
		if r.batch == len(r.cumulative)-1 {
			r.state = StateCompleted
			return ObserveResult{Accepted: true, Outcome: OutcomeCompleted}, nil
		}
		r.batch++
		r.startTime = now
		r.resetCountersLocked()
		return ObserveResult{Accepted: true, Outcome: OutcomeAdvanced}, nil
	}

	r.failures++
	r.passStreak = 0
	if r.failures < r.failureLimit {
		return ObserveResult{Accepted: true, Outcome: OutcomeFailed}, nil
	}

	r.rollbackCount++
	if r.batch == 0 {
		r.state = StateAborted
		return ObserveResult{Accepted: true, Outcome: OutcomeAborted}, nil
	}

	r.batch--
	r.startTime = now + r.silence
	r.resetCountersLocked()
	if r.rollbackCount >= r.rollbackLimit {
		r.state = StateFrozen
		return ObserveResult{Accepted: true, Outcome: OutcomeFrozen}, nil
	}
	return ObserveResult{Accepted: true, Outcome: OutcomeRolledBack}, nil
}

// Status returns a concurrency-safe copy of the current state.
func (r *Reviewer) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()

	instances := int64(0)
	if r.state == StateCompleted {
		instances = r.totalInstances
	} else if r.state == StateRunning || r.state == StateFrozen {
		instances = r.cumulative[r.batch]
	}
	return Status{
		State:            r.state,
		Batch:            r.batch,
		Instances:        instances,
		StartTime:        r.startTime,
		NewRequests:      r.newRequests,
		NewErrors:        r.newErrors,
		BaselineRequests: r.baseRequests,
		BaselineErrors:   r.baseErrors,
		PassStreak:       r.passStreak,
		Failures:         r.failures,
		RollbackCount:    r.rollbackCount,
	}
}

func ceilDiv(value, divisor int64) int64 {
	return (value + divisor - 1) / divisor
}

func validateIncrement(requests, errors int64) error {
	if requests < 0 || requests > 1_000_000_000 || errors < 0 || errors > 1_000_000_000 || errors > requests {
		return ErrInvalidArgument
	}
	return nil
}

func gateFails(newErrors, baselineRequests, newRequests, baselineErrors, toleranceFactor int64) bool {
	left := new(big.Int).Mul(big.NewInt(newErrors), big.NewInt(baselineRequests))
	left.Mul(left, big.NewInt(100))

	right := new(big.Int).Mul(big.NewInt(baselineErrors), big.NewInt(newRequests))
	right.Mul(right, big.NewInt(toleranceFactor))

	return left.Cmp(right) > 0
}

func (r *Reviewer) resetCountersLocked() {
	r.newRequests = 0
	r.newErrors = 0
	r.baseRequests = 0
	r.baseErrors = 0
	r.passStreak = 0
	r.failures = 0
}
