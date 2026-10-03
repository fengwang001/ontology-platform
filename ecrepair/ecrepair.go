// Package ecrepair simulates a lazy repair scheduler for erasure-coded
// stripes. It only simulates scheduling decisions (which stripe gets
// repaired, when, and at what token cost); no encoding math is performed.
package ecrepair

import (
	"errors"
	"sort"
	"sync"
)

// ShardState is the state of a single shard within a stripe.
type ShardState int

const (
	Alive ShardState = iota
	Lost
	Rebuilding
)

// StripeState is the aggregate state of a stripe.
type StripeState int

const (
	Healthy StripeState = iota
	Degraded
	Repairing
	Dead
)

// Distinguishable errors returned by the scheduler.
var (
	ErrParam    = errors.New("ecrepair: invalid parameter")
	ErrClock    = errors.New("ecrepair: clock regression")
	ErrExists   = errors.New("ecrepair: stripe already exists")
	ErrUnknown  = errors.New("ecrepair: unknown stripe")
	ErrDead     = errors.New("ecrepair: stripe is dead")
	ErrNotAlive = errors.New("ecrepair: shard is not alive")
)

// RepairStart describes one repair launched by Tick.
type RepairStart struct {
	ID     int
	Shards []int
	Finish int
}

// Report is the result of one atomic Tick.
type Report struct {
	Completed []int
	Started   []RepairStart
}

// Status is a point-in-time snapshot of one stripe.
type Status struct {
	State      StripeState
	Alive      int
	Lost       int
	Rebuilding int
	Margin     int
	FirstLost  int // -1 when the stripe has no degradation origin
}

// Scheduler maintains per-stripe state, the repair token bucket and the
// in-flight repair set. All methods are safe for concurrent use and behave
// as if executed in some serial order; Tick is one atomic step.
type Scheduler struct {
	mu sync.Mutex

	k, m, n  int
	tau      int
	ageLimit int
	rate     int
	cap      int
	dur      int
	maxInfl  int

	now      int
	lastTick int
	tokens   int
	stripes  map[int]*stripe
}

// New creates a scheduler for k data + m parity shards per stripe.
func New(k, m, tau, A, R, Cap, D, Q int) (*Scheduler, error) {
	if k < 1 || m < 1 || k+m > 32 || tau < 0 || tau > m ||
		A < 1 || R < 1 || Cap < R || Cap > 1e9 || D < 1 || Q < 1 {
		return nil, ErrParam
	}
	return &Scheduler{
		k: k, m: m, n: k + m,
		tau: tau, ageLimit: A,
		rate: R, cap: Cap,
		dur: D, maxInfl: Q,
		stripes: make(map[int]*stripe),
	}, nil
}

// AddStripe creates a stripe with all shards Alive.
func (s *Scheduler) AddStripe(now, id int) error {
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
	s.stripes[id] = newStripe(s.n)
	s.now = now
	return nil
}

// Lose marks one Alive shard of a stripe as Lost.
func (s *Scheduler) Lose(now, id, shard int) error {
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
	_, lost, rebuilding := st.counts()
	if lost == 0 && rebuilding == 0 {
		st.firstLost = now
	}
	st.shards[shard] = Lost
	s.now = now
	s.checkDead(st)
	return nil
}

// Tick advances the scheduler to now in one atomic step.
func (s *Scheduler) Tick(now int) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now < 0 {
		return Report{}, ErrParam
	}
	if now < s.now {
		return Report{}, ErrClock
	}

	rep := Report{Completed: []int{}, Started: []RepairStart{}}

	// Step 1: finish repairs with finish <= now, ordered by (finish, id).
	type done struct {
		id     int
		finish int
	}
	var finishing []done
	for id, st := range s.stripes {
		if st.inflight != nil && st.inflight.finish <= now {
			finishing = append(finishing, done{id, st.inflight.finish})
		}
	}
	sort.Slice(finishing, func(i, j int) bool {
		if finishing[i].finish != finishing[j].finish {
			return finishing[i].finish < finishing[j].finish
		}
		return finishing[i].id < finishing[j].id
	})
	for _, d := range finishing {
		st := s.stripes[d.id]
		for _, sh := range st.inflight.targets {
			st.shards[sh] = Alive
		}
		st.inflight = nil
		rep.Completed = append(rep.Completed, d.id)
		s.checkDead(st)
		if _, lost, _ := st.counts(); lost == 0 {
			st.firstLost = -1
		}
	}

	// Step 2: accumulate tokens.
	if delta := now - s.lastTick; delta > 0 {
		s.tokens += s.rate * delta
		if s.tokens > s.cap {
			s.tokens = s.cap
		}
	}
	s.lastTick = now

	// Step 3: scheduling loop.
	for {
		if s.inflightCount() >= s.maxInfl {
			break
		}
		cand, ok := s.bestCandidate(now)
		if !ok {
			break
		}
		if s.tokens < s.k {
			break
		}
		s.tokens -= s.k
		st := s.stripes[cand]
		var targets []int
		for sh, state := range st.shards {
			if state == Lost {
				st.shards[sh] = Rebuilding
				targets = append(targets, sh)
			}
		}
		finish := now + s.dur
		st.inflight = &repair{targets: targets, finish: finish}
		rep.Started = append(rep.Started, RepairStart{ID: cand, Shards: targets, Finish: finish})
	}

	s.now = now
	return rep, nil
}

// Status returns a snapshot of one stripe.
func (s *Scheduler) Status(id int) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id <= 0 {
		return Status{}, ErrParam
	}
	st, ok := s.stripes[id]
	if !ok {
		return Status{}, ErrUnknown
	}
	alive, lost, rebuilding := st.counts()
	return Status{
		State:      s.stateOf(st),
		Alive:      alive,
		Lost:       lost,
		Rebuilding: rebuilding,
		Margin:     alive - s.k,
		FirstLost:  st.firstLost,
	}, nil
}

// Tokens returns the current token balance.
func (s *Scheduler) Tokens() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens
}

// checkDead marks a stripe Dead when it is unrecoverable and has no
// in-flight repair. Dead is sticky.
func (s *Scheduler) checkDead(st *stripe) {
	if st.dead || st.inflight != nil {
		return
	}
	if alive, _, _ := st.counts(); alive < s.k {
		st.dead = true
	}
}

// stateOf derives the aggregate stripe state.
func (s *Scheduler) stateOf(st *stripe) StripeState {
	if st.dead {
		return Dead
	}
	_, lost, rebuilding := st.counts()
	if rebuilding > 0 {
		return Repairing
	}
	if lost > 0 {
		return Degraded
	}
	return Healthy
}

// inflightCount returns the number of stripes with an in-flight repair.
func (s *Scheduler) inflightCount() int {
	n := 0
	for _, st := range s.stripes {
		if st.inflight != nil {
			n++
		}
	}
	return n
}

// bestCandidate picks the next stripe to repair: tier 0 holds stripes whose
// degradation age reached the limit, tier 1 the rest; ordering is
// (tier, margin, firstLost, id), all ascending.
func (s *Scheduler) bestCandidate(now int) (int, bool) {
	type cand struct {
		id        int
		tier      int
		margin    int
		firstLost int
	}
	var cands []cand
	for id, st := range s.stripes {
		if st.dead || st.inflight != nil {
			continue
		}
		alive, lost, _ := st.counts()
		if lost == 0 {
			continue
		}
		margin := alive - s.k
		age := now - st.firstLost
		if margin > s.tau && age < s.ageLimit {
			continue
		}
		tier := 1
		if age >= s.ageLimit {
			tier = 0
		}
		cands = append(cands, cand{id, tier, margin, st.firstLost})
	}
	if len(cands) == 0 {
		return 0, false
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.margin != b.margin {
			return a.margin < b.margin
		}
		if a.firstLost != b.firstLost {
			return a.firstLost < b.firstLost
		}
		return a.id < b.id
	})
	return cands[0].id, true
}
