// Package scheduler implements a fair-share job scheduler whose per-account
// accumulated usage decays by integer halving at fixed period boundaries.
//
// All arithmetic is exact: usage is kept as a big.Int, and ratio comparisons
// use cross-multiplication, so results are fully deterministic and
// reproducible. A Scheduler is safe for concurrent use; the result of any
// interleaving of calls is equivalent to some serial order.
package scheduler

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// Rejection reasons. Every rejected operation leaves usage, queues and the
// number of applied boundaries untouched.
var (
	// ErrClockRollback: the call's time is earlier than a previously seen time.
	ErrClockRollback = errors.New("scheduler: clock moved backwards")
	// ErrNonPositivePeriod: decay period must be a positive number of milliseconds.
	ErrNonPositivePeriod = errors.New("scheduler: period must be positive")
	// ErrNonPositiveShare: account share must be a positive integer.
	ErrNonPositiveShare = errors.New("scheduler: share must be positive")
	// ErrDuplicateAccount: the account is already registered.
	ErrDuplicateAccount = errors.New("scheduler: account already registered")
	// ErrUnknownAccount: the account is not registered.
	ErrUnknownAccount = errors.New("scheduler: account not registered")
	// ErrNonPositiveCost: job cost must be a positive integer.
	ErrNonPositiveCost = errors.New("scheduler: job cost must be positive")
	// ErrDuplicateJobID: the job ID was already submitted.
	ErrDuplicateJobID = errors.New("scheduler: duplicate job ID")
	// ErrNoJobs: dispatch was requested while every queue is empty.
	ErrNoJobs = errors.New("scheduler: no queued jobs")
)

// Job is a unit of work with a positive integer cost.
type Job struct {
	ID   string
	Cost int64
}

// Candidate describes one account that was eligible for a dispatch.
type Candidate struct {
	AccountID string
	Usage     *big.Int // usage after boundary decay, before charging the job
	Share     int64
}

// DispatchResult reports the dispatched job and the full decision basis.
type DispatchResult struct {
	Job               Job
	AccountID         string
	Candidates        []Candidate // sorted by account ID ascending
	Reason            string
	BoundariesApplied int64 // total boundaries applied so far, including this call
}

// AccountSnapshot is a read-only view of one account.
type AccountSnapshot struct {
	AccountID string
	Share     int64
	Usage     *big.Int
	Queue     []Job
}

// Snapshot is a consistent read-only view of the scheduler.
type Snapshot struct {
	LastTime          int64
	BoundariesApplied int64
	Accounts          []AccountSnapshot // sorted by account ID ascending
}

type account struct {
	id    string
	share int64
	usage *big.Int
	queue []Job
}

// Scheduler is a decaying fair-share job scheduler.
type Scheduler struct {
	mu                sync.Mutex
	t0                int64
	period            int64
	lastTime          int64
	seenTime          bool
	boundariesApplied int64
	accounts          map[string]*account
	jobIDs            map[string]struct{}
}

// New creates a scheduler with origin t0 and decay period P (both in
// milliseconds). Boundaries are t0+k*P for positive integers k.
func New(t0, period int64) (*Scheduler, error) {
	if period <= 0 {
		return nil, ErrNonPositivePeriod
	}
	return &Scheduler{
		t0:       t0,
		period:   period,
		accounts: make(map[string]*account),
		jobIDs:   make(map[string]struct{}),
	}, nil
}

// Register adds an account with a positive integer share at time t.
func (s *Scheduler) Register(t int64, accountID string, share int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	if share <= 0 {
		return ErrNonPositiveShare
	}
	if _, ok := s.accounts[accountID]; ok {
		return ErrDuplicateAccount
	}
	s.applyBoundaries(t)
	s.accounts[accountID] = &account{id: accountID, share: share, usage: new(big.Int)}
	return nil
}

// Submit enqueues a job for a registered account at time t.
func (s *Scheduler) Submit(t int64, accountID string, job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	acct, ok := s.accounts[accountID]
	if !ok {
		return ErrUnknownAccount
	}
	if job.Cost <= 0 {
		return ErrNonPositiveCost
	}
	if _, ok := s.jobIDs[job.ID]; ok {
		return ErrDuplicateJobID
	}
	s.applyBoundaries(t)
	acct.queue = append(acct.queue, job)
	s.jobIDs[job.ID] = struct{}{}
	return nil
}

// Dispatch picks the head job of the non-empty account with the smallest
// usage/share ratio (ties broken by account ID ascending) at time t, charges
// the job cost to that account's usage immediately, and returns the job with
// the decision basis.
func (s *Scheduler) Dispatch(t int64) (*DispatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return nil, err
	}
	empty := true
	for _, acct := range s.accounts {
		if len(acct.queue) > 0 {
			empty = false
			break
		}
	}
	if empty {
		return nil, ErrNoJobs
	}
	s.applyBoundaries(t)
	return s.dispatchLocked(), nil
}

// Snapshot returns a consistent view of the scheduler at time t, applying
// any boundaries up to and including t first.
func (s *Scheduler) Snapshot(t int64) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return Snapshot{}, err
	}
	s.applyBoundaries(t)
	return s.snapshotLocked(), nil
}

func (s *Scheduler) checkClock(t int64) error {
	if s.seenTime && t < s.lastTime {
		return ErrClockRollback
	}
	s.seenTime = true
	s.lastTime = t
	return nil
}

func (s *Scheduler) applyBoundaries(t int64) {
	if t < s.t0 {
		return
	}
	// Number of boundaries due at t: floor((t - t0) / P). Both operands are
	// non-negative, so integer division is exact floor division and no
	// boundary-time multiplication can overflow.
	due := (t - s.t0) / s.period
	k := due - s.boundariesApplied
	if k <= 0 {
		return
	}
	s.boundariesApplied = due
	for _, acct := range s.accounts {
		halve(acct.usage, k)
	}
}

// halve sets u = floor(u / 2^k), exactly equivalent to k sequential
// integer halvings, for non-negative u.
func halve(u *big.Int, k int64) {
	if u.Sign() == 0 {
		return
	}
	if k >= int64(u.BitLen()) {
		u.SetInt64(0)
		return
	}
	u.Rsh(u, uint(k))
}

func (s *Scheduler) dispatchLocked() *DispatchResult {
	ids := make([]string, 0, len(s.accounts))
	for id, acct := range s.accounts {
		if len(acct.queue) > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	candidates := make([]Candidate, 0, len(ids))
	best := s.accounts[ids[0]]
	tied := false
	for _, id := range ids {
		acct := s.accounts[id]
		candidates = append(candidates, Candidate{
			AccountID: id,
			Usage:     new(big.Int).Set(acct.usage),
			Share:     acct.share,
		})
		if acct == best {
			continue
		}
		switch compareRatio(acct.usage, acct.share, best.usage, best.share) {
		case -1:
			best = acct
			tied = false
		case 0:
			tied = true // ids are ascending: the earlier ID wins the tie
		}
	}

	job := best.queue[0]
	best.queue = best.queue[1:]
	best.usage.Add(best.usage, big.NewInt(job.Cost))

	reason := fmt.Sprintf(
		"account %q has the smallest usage/share ratio (U=%s, share=%d) among %d non-empty queue(s)",
		best.id, candidates[indexOf(candidates, best.id)].Usage, best.share, len(candidates))
	if tied {
		reason += "; ratio tied, broken by account ID ascending"
	}
	reason += fmt.Sprintf("; cost %d charged to account %q immediately", job.Cost, best.id)

	return &DispatchResult{
		Job:               job,
		AccountID:         best.id,
		Candidates:        candidates,
		Reason:            reason,
		BoundariesApplied: s.boundariesApplied,
	}
}

// compareRatio compares u1/s1 with u2/s2 by cross-multiplication:
// u1*s2 vs u2*s1. Exact big-integer arithmetic, no floats, no division.
func compareRatio(u1 *big.Int, s1 int64, u2 *big.Int, s2 int64) int {
	left := new(big.Int).Mul(u1, big.NewInt(s2))
	right := new(big.Int).Mul(u2, big.NewInt(s1))
	return left.Cmp(right)
}

func indexOf(candidates []Candidate, id string) int {
	for i, c := range candidates {
		if c.AccountID == id {
			return i
		}
	}
	return -1
}

func (s *Scheduler) snapshotLocked() Snapshot {
	snap := Snapshot{
		LastTime:          s.lastTime,
		BoundariesApplied: s.boundariesApplied,
		Accounts:          make([]AccountSnapshot, 0, len(s.accounts)),
	}
	for _, acct := range s.accounts {
		snap.Accounts = append(snap.Accounts, AccountSnapshot{
			AccountID: acct.id,
			Share:     acct.share,
			Usage:     new(big.Int).Set(acct.usage),
			Queue:     append([]Job(nil), acct.queue...),
		})
	}
	sort.Slice(snap.Accounts, func(i, j int) bool {
		return snap.Accounts[i].AccountID < snap.Accounts[j].AccountID
	})
	return snap
}
