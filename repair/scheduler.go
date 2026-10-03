// Package repair implements a deterministic delayed-repair scheduler for
// erasure-coded stripes. It only simulates scheduling: no encoding or
// decoding arithmetic is performed.
package repair

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrParam    = errors.New("repair: invalid parameter")
	ErrClock    = errors.New("repair: clock moved backwards")
	ErrUnknown  = errors.New("repair: unknown stripe")
	ErrExists   = errors.New("repair: stripe already exists")
	ErrDead     = errors.New("repair: stripe is dead")
	ErrNotAlive = errors.New("repair: shard is not alive")
)

// ShardState is the state of a single shard.
type ShardState int

const (
	Alive ShardState = iota
	Lost
	Rebuilding
)

// State is the lifecycle state of a stripe.
type State int

const (
	Healthy State = iota
	Degraded
	Repairing
	Dead
)

// Start describes one repair launched by Tick.
type Start struct {
	Stripe  int64
	Targets []int
	Finish  int64
}

// Report is the outcome of one Tick.
type Report struct {
	Completed []int64
	Started   []Start
}

// Status is a point-in-time snapshot of a stripe.
type Status struct {
	State      State
	Alive      int
	Lost       int
	Rebuilding int
	Margin     int
	FirstLost  int64
}

const maxShards = 32

type stripe struct {
	shards    [maxShards]ShardState
	firstLost int64 // -1 when the stripe carries no degradation timestamp
	inFlight  bool
	finish    int64
	targets   []int
	dead      bool
}

// Scheduler keeps per-stripe state and the token bucket. All methods are
// safe for concurrent use; every call behaves as an atomic serial step.
type Scheduler struct {
	mu       sync.Mutex
	k        int
	n        int
	tau      int
	A        int64
	R        int64
	Cap      int64
	D        int64
	Q        int
	now      int64
	lastTick int64
	tokens   int64
	stripes  map[int64]*stripe
}

// New creates a scheduler. Parameter constraints:
// 1 <= k, 1 <= m, k+m <= 32, 0 <= tau <= m, A >= 1, R >= 1,
// R <= Cap <= 1e9, D >= 1, Q >= 1. Any violation yields ErrParam.
func New(k, m, tau int, A, R, Cap, D, Q int64) (*Scheduler, error) {
	if k < 1 || m < 1 || k+m > maxShards || tau < 0 || tau > m ||
		A < 1 || R < 1 || Cap < R || Cap > 1e9 || D < 1 || Q < 1 {
		return nil, ErrParam
	}
	return &Scheduler{
		k:        k,
		n:        k + m,
		tau:      tau,
		A:        A,
		R:        R,
		Cap:      Cap,
		D:        D,
		Q:        int(Q),
		stripes:  make(map[int64]*stripe),
		lastTick: 0,
	}, nil
}

// AddStripe creates a stripe whose shards are all Alive.
func (s *Scheduler) AddStripe(now, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || id <= 0 {
		return ErrParam
	}
	if now < s.now {
		return ErrClock
	}
	if _, ok := s.stripes[id]; ok {
		return ErrExists
	}
	st := &stripe{firstLost: -1}
	s.stripes[id] = st
	s.now = now
	return nil
}

// Lose marks one Alive shard of a stripe as Lost.
func (s *Scheduler) Lose(now, id int64, shard int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 || id <= 0 || shard < 0 || shard >= s.n {
		return ErrParam
	}
	if now < s.now {
		return ErrClock
	}
	st, ok := s.stripes[id]
	if !ok {
		return ErrUnknown
	}
	if st.dead {
		return ErrDead
	}
	if st.shards[shard] != Alive {
		return ErrNotAlive
	}
	hadDamage := false
	for i := 0; i < s.n; i++ {
		if st.shards[i] != Alive {
			hadDamage = true
			break
		}
	}
	st.shards[shard] = Lost
	if !hadDamage {
		st.firstLost = now
	}
	s.judgeDead(st)
	s.now = now
	return nil
}

// Tick advances the scheduler to now and runs the four fixed steps:
// completions, token accrual, the scheduling loop, and reporting.
func (s *Scheduler) Tick(now int64) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return Report{}, ErrParam
	}
	if now < s.now {
		return Report{}, ErrClock
	}
	rep := Report{Completed: []int64{}, Started: []Start{}}

	// Step 1: finish repairs with finish <= now, ordered by (finish, id).
	type pending struct {
		id     int64
		finish int64
	}
	var done []pending
	for id, st := range s.stripes {
		if st.inFlight && st.finish <= now {
			done = append(done, pending{id: id, finish: st.finish})
		}
	}
	sort.Slice(done, func(i, j int) bool {
		if done[i].finish != done[j].finish {
			return done[i].finish < done[j].finish
		}
		return done[i].id < done[j].id
	})
	for _, p := range done {
		st := s.stripes[p.id]
		for _, sh := range st.targets {
			st.shards[sh] = Alive
		}
		st.targets = nil
		st.inFlight = false
		s.judgeDead(st)
		if s.lostCount(st) == 0 {
			st.firstLost = -1
		}
		rep.Completed = append(rep.Completed, p.id)
	}

	// Step 2: token accrual, capped at Cap.
	s.tokens += s.R * (now - s.lastTick)
	if s.tokens > s.Cap {
		s.tokens = s.Cap
	}
	s.lastTick = now

	// Step 3: scheduling loop.
	for {
		if s.inFlightCount() >= s.Q {
			break
		}
		best, found := s.pickCandidate(now)
		if !found {
			break
		}
		if s.tokens < int64(s.k) {
			break
		}
		s.tokens -= int64(s.k)
		st := s.stripes[best]
		targets := []int{}
		for i := 0; i < s.n; i++ {
			if st.shards[i] == Lost {
				st.shards[i] = Rebuilding
				targets = append(targets, i)
			}
		}
		st.inFlight = true
		st.targets = targets
		st.finish = now + s.D
		rep.Started = append(rep.Started, Start{Stripe: best, Targets: targets, Finish: st.finish})
	}

	// Step 4: report.
	s.now = now
	return rep, nil
}

// Status returns the snapshot of one stripe.
func (s *Scheduler) Status(id int64) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id <= 0 {
		return Status{}, ErrParam
	}
	st, ok := s.stripes[id]
	if !ok {
		return Status{}, ErrUnknown
	}
	stt := Status{
		Alive:      s.aliveCount(st),
		Lost:       s.lostCount(st),
		Rebuilding: s.rebuildingCount(st),
		FirstLost:  st.firstLost,
	}
	stt.Margin = stt.Alive - s.k
	switch {
	case st.dead:
		stt.State = Dead
	case st.inFlight:
		stt.State = Repairing
	case stt.Lost > 0:
		stt.State = Degraded
	default:
		stt.State = Healthy
	}
	return stt, nil
}

// Tokens returns the current token balance.
func (s *Scheduler) Tokens() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens
}

func (s *Scheduler) aliveCount(st *stripe) int {
	c := 0
	for i := 0; i < s.n; i++ {
		if st.shards[i] == Alive {
			c++
		}
	}
	return c
}

func (s *Scheduler) lostCount(st *stripe) int {
	c := 0
	for i := 0; i < s.n; i++ {
		if st.shards[i] == Lost {
			c++
		}
	}
	return c
}

func (s *Scheduler) rebuildingCount(st *stripe) int {
	c := 0
	for i := 0; i < s.n; i++ {
		if st.shards[i] == Rebuilding {
			c++
		}
	}
	return c
}

func (s *Scheduler) inFlightCount() int {
	c := 0
	for _, st := range s.stripes {
		if st.inFlight {
			c++
		}
	}
	return c
}

// judgeDead marks a stripe Dead when it has too few Alive shards and no
// repair in flight. Dead is sticky.
func (s *Scheduler) judgeDead(st *stripe) {
	if !st.dead && !st.inFlight && s.aliveCount(st) < s.k {
		st.dead = true
	}
}

// pickCandidate selects the next stripe to repair: tier 0 holds stripes
// whose degradation age reached A, tier 1 the rest; ordering is
// (tier, margin, firstLost, id), all ascending.
func (s *Scheduler) pickCandidate(now int64) (int64, bool) {
	var bestID int64
	bestTier, bestMargin := 0, 0
	var bestFirst int64
	found := false
	for id, st := range s.stripes {
		if st.dead || st.inFlight || s.lostCount(st) == 0 {
			continue
		}
		margin := s.aliveCount(st) - s.k
		aged := now-st.firstLost >= s.A
		if margin > s.tau && !aged {
			continue
		}
		tier := 1
		if aged {
			tier = 0
		}
		if !found ||
			tier < bestTier ||
			(tier == bestTier && margin < bestMargin) ||
			(tier == bestTier && margin == bestMargin && st.firstLost < bestFirst) ||
			(tier == bestTier && margin == bestMargin && st.firstLost == bestFirst && id < bestID) {
			found = true
			bestID = id
			bestTier = tier
			bestMargin = margin
			bestFirst = st.firstLost
		}
	}
	return bestID, found
}
