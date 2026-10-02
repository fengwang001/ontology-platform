// Package scheduler implements a fair-share job scheduler whose per-account
// accumulated usage decays by integer halving at fixed period boundaries.
//
// All arithmetic is exact: usage is kept as a big integer and ratio
// comparisons are done by cross-multiplication, never by floating point or
// integer division. Replaying the same serialised operation sequence yields
// exactly the same dispatch order and usage values.
package scheduler

import (
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// Job is a unit of work submitted to an account queue.
type Job struct {
	ID      string
	Account string
	Cost    int64
}

// account is the internal per-account state.
type account struct {
	id    string
	share int64
	usage *big.Int
	queue []Job
}

// Scheduler is safe for concurrent use; every operation is serialised
// internally, so concurrent calls are equivalent to some serial order.
type Scheduler struct {
	mu         sync.Mutex
	t0         int64
	period     int64
	lastTime   int64
	boundaries int64
	accounts   map[string]*account
	seenJobIDs map[string]struct{}
}

// New creates a scheduler anchored at start time t0 (milliseconds) with
// decay period P (milliseconds, must be positive).
func New(t0, period int64) (*Scheduler, error) {
	if period <= 0 {
		return nil, reject(ReasonInvalidPeriod, "period must be a positive integer, got %d", period)
	}
	return &Scheduler{
		t0:         t0,
		period:     period,
		lastTime:   t0,
		accounts:   make(map[string]*account),
		seenJobIDs: make(map[string]struct{}),
	}, nil
}

// checkClock rejects calls whose time is earlier than any previously seen
// time. It must run before any other validation and before any state change.
func (s *Scheduler) checkClock(t int64) error {
	if t < s.lastTime {
		return reject(ReasonClockBackwards, "time %d is earlier than last seen time %d", t, s.lastTime)
	}
	return nil
}

// advance applies every decay boundary in (lastTime, t] and moves the clock
// forward. A boundary t0+k*P (k a positive integer) takes effect exactly at
// its own instant; each boundary halves every account's usage with integer
// floor division by 2, applied one boundary at a time. Halving k times in a
// row equals a single floor division by 2^k, so the shift below is exact.
func (s *Scheduler) advance(t int64) {
	if t > s.lastTime {
		s.lastTime = t
	}
	if t < s.t0 {
		return
	}
	due := (t - s.t0) / s.period
	k := due - s.boundaries
	if k <= 0 {
		return
	}
	s.boundaries = due
	for _, acc := range s.accounts {
		if acc.usage.Sign() == 0 {
			continue
		}
		if k >= int64(acc.usage.BitLen()) {
			acc.usage.SetInt64(0)
		} else {
			acc.usage.Rsh(acc.usage, uint(k))
		}
	}
}

// Register adds an account with a positive integer share.
func (s *Scheduler) Register(t int64, id string, share int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	if share <= 0 {
		return reject(ReasonInvalidShare, "share must be a positive integer, got %d", share)
	}
	if _, ok := s.accounts[id]; ok {
		return reject(ReasonDuplicateAccount, "account %q is already registered", id)
	}
	s.advance(t)
	s.accounts[id] = &account{id: id, share: share, usage: new(big.Int)}
	return nil
}

// Submit enqueues a job for a registered account.
func (s *Scheduler) Submit(t int64, accountID, jobID string, cost int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	acc, ok := s.accounts[accountID]
	if !ok {
		return reject(ReasonAccountNotRegistered, "account %q is not registered", accountID)
	}
	if cost <= 0 {
		return reject(ReasonInvalidCost, "cost must be a positive integer, got %d", cost)
	}
	if _, ok := s.seenJobIDs[jobID]; ok {
		return reject(ReasonDuplicateJobID, "job ID %q was already submitted", jobID)
	}
	s.advance(t)
	s.seenJobIDs[jobID] = struct{}{}
	acc.queue = append(acc.queue, Job{ID: jobID, Account: accountID, Cost: cost})
	return nil
}

// Dispatch picks the head job of the candidate account with the minimal
// usage/share ratio and charges its cost immediately.
func (s *Scheduler) Dispatch(t int64) (*DispatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(s.accounts))
	for id, acc := range s.accounts {
		if len(acc.queue) > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, reject(ReasonNoJobs, "all account queues are empty")
	}
	s.advance(t)
	sort.Strings(ids)

	candidates := make([]Candidate, 0, len(ids))
	var winner *account
	for _, id := range ids {
		acc := s.accounts[id]
		candidates = append(candidates, Candidate{
			AccountID: id,
			Usage:     new(big.Int).Set(acc.usage),
			Share:     acc.share,
		})
		if winner == nil || lessRatio(acc, winner) {
			winner = acc
		}
	}

	reason := fmt.Sprintf("account %q has the minimal usage/share ratio among %d candidate(s)",
		winner.id, len(candidates))
	if tied := countTied(candidates, winner); tied > 1 {
		reason = fmt.Sprintf("account %q ties with %d other candidate(s) on usage/share ratio and wins by smallest account ID",
			winner.id, tied-1)
	}

	job := winner.queue[0]
	winner.queue = winner.queue[1:]
	winner.usage.Add(winner.usage, big.NewInt(job.Cost))

	return &DispatchResult{
		Job:        job,
		Winner:     winner.id,
		Candidates: candidates,
		Reason:     reason,
	}, nil
}

// Query returns a consistent snapshot of the scheduler state at time t.
func (s *Scheduler) Query(t int64) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return nil, err
	}
	s.advance(t)

	ids := make([]string, 0, len(s.accounts))
	for id := range s.accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	snap := &Snapshot{
		Time:              s.lastTime,
		BoundariesApplied: s.boundaries,
		Accounts:          make([]AccountSnapshot, 0, len(ids)),
	}
	for _, id := range ids {
		acc := s.accounts[id]
		queued := make([]string, len(acc.queue))
		for i, job := range acc.queue {
			queued[i] = job.ID
		}
		snap.Accounts = append(snap.Accounts, AccountSnapshot{
			ID:       id,
			Share:    acc.share,
			Usage:    new(big.Int).Set(acc.usage),
			QueueLen: len(acc.queue),
			Queued:   queued,
		})
	}
	return snap, nil
}

// lessRatio reports whether a's usage/share ratio is strictly smaller than
// b's, comparing by exact cross-multiplication: Ua*Sb < Ub*Sa. No floating
// point and no division are involved, and big integers keep the products
// exact no matter how large they grow.
func lessRatio(a, b *account) bool {
	left := new(big.Int).Mul(a.usage, big.NewInt(b.share))
	right := new(big.Int).Mul(b.usage, big.NewInt(a.share))
	return left.Cmp(right) < 0
}

// countTied counts candidates whose usage/share ratio equals the winner's.
func countTied(candidates []Candidate, winner *account) int {
	tied := 0
	for _, c := range candidates {
		left := new(big.Int).Mul(c.Usage, big.NewInt(winner.share))
		right := new(big.Int).Mul(winner.usage, big.NewInt(c.Share))
		if left.Cmp(right) == 0 {
			tied++
		}
	}
	return tied
}
