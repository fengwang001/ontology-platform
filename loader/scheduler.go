package loader

import (
	"fmt"
	"sync"
)

// Scheduler coordinates registration, quotas, preemption, the preload
// cache and completion notification. A single mutex serializes all
// mutations, so concurrent calls are equivalent to some serial order.
type Scheduler struct {
	mu             sync.Mutex
	cfg            Config
	now            int64
	seq            uint64
	startSeq       uint64
	nextID         uint64
	reqs           map[uint64]*request
	pending        *pendingSet
	byOrigin       map[Origin]map[uint64]*request // transferring only
	total          int
	activePreloads map[cacheKey]*request // registered, non-terminal preloads
	cache          *preloadCache
}

// NewScheduler validates the limits; zero limits are invalid arguments.
func NewScheduler(cfg Config) (*Scheduler, error) {
	if cfg.PerOriginLimit <= 0 {
		return nil, fmt.Errorf("%w: per-origin limit must be positive", ErrInvalidArgument)
	}
	if cfg.GlobalLimit <= 0 {
		return nil, fmt.Errorf("%w: global limit must be positive", ErrInvalidArgument)
	}
	if cfg.MaxPauses < 0 {
		return nil, fmt.Errorf("%w: max pauses must be non-negative", ErrInvalidArgument)
	}
	if cfg.PreloadTTL < 0 {
		return nil, fmt.Errorf("%w: preload TTL must be non-negative", ErrInvalidArgument)
	}
	return &Scheduler{
		cfg:            cfg,
		reqs:           make(map[uint64]*request),
		pending:        newPendingSet(),
		byOrigin:       make(map[Origin]map[uint64]*request),
		activePreloads: make(map[cacheKey]*request),
		cache:          newPreloadCache(cfg.PreloadTTL),
	}, nil
}

// Now reports the current logical clock.
func (s *Scheduler) Now() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// setClockLocked advances the clock and expires wasted cache entries.
func (s *Scheduler) setClockLocked(at int64) {
	s.now = at
	s.cache.sweep(at)
}

// Register validates, then either serves the request from the preload
// cache, attaches it to an in-flight preload, or queues it for scheduling.
// Validation order: invalid argument, then clock skew. A rejected call
// changes nothing.
func (s *Scheduler) Register(at int64, in RequestInput) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if in.URL == "" {
		return 0, fmt.Errorf("%w: empty URL", ErrInvalidArgument)
	}
	if !in.Type.valid() {
		return 0, fmt.Errorf("%w: unknown resource type %d", ErrInvalidArgument, int(in.Type))
	}
	if !in.Priority.valid() {
		return 0, fmt.Errorf("%w: priority out of range: %d", ErrInvalidArgument, int(in.Priority))
	}
	if in.Size <= 0 {
		return 0, fmt.Errorf("%w: size must be positive", ErrInvalidArgument)
	}
	key := cacheKey{in.Origin, in.URL}
	if in.Type == TypePreload {
		if !in.As.valid() || in.As == TypePreload {
			return 0, fmt.Errorf("%w: preload destination type invalid", ErrInvalidArgument)
		}
		if _, ok := s.activePreloads[key]; ok || s.cache.has(key) {
			return 0, fmt.Errorf("%w: duplicate preload registration for %q", ErrInvalidArgument, in.URL)
		}
	}
	if at < s.now {
		return 0, fmt.Errorf("%w: at=%d before now=%d", ErrClockSkew, at, s.now)
	}

	s.setClockLocked(at)
	s.seq++
	s.nextID++
	r := &request{id: s.nextID, seq: s.seq, in: in, state: StatePending}
	s.reqs[r.id] = r

	if in.Type == TypePreload {
		s.activePreloads[key] = r
	} else if s.cache.hit(in) {
		// Cache hit: served immediately, entry consumed.
		s.cache.take(in)
		r.state = StateCompleted
		r.progress = in.Size
		r.fromCache = true
		return r.id, nil
	} else if p, ok := s.activePreloads[key]; ok && p.startSeq > 0 {
		// In-flight preload with the same key: attach and share its fate.
		r.state = StateAttached
		r.attachedTo = p
		p.attachers = append(p.attachers, r)
		s.pending.syncTier(p) // effective priority may have risen
		s.fixOriginLocked(p.in.Origin)
		s.scheduleLocked()
		return r.id, nil
	}

	s.pending.add(r, false)
	s.fixOriginLocked(in.Origin)
	s.scheduleLocked()
	return r.id, nil
}

// Advance adds transferred bytes to a transferring request; reaching Size
// completes it. Progress never moves backwards.
func (s *Scheduler) Advance(id uint64, delta int64, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == 0 || delta <= 0 {
		return fmt.Errorf("%w: id and positive delta required", ErrInvalidArgument)
	}
	if at < s.now {
		return fmt.Errorf("%w: at=%d before now=%d", ErrClockSkew, at, s.now)
	}
	r, ok := s.reqs[id]
	if !ok {
		return fmt.Errorf("%w: id=%d", ErrNotFound, id)
	}
	if r.state != StateTransferring {
		return fmt.Errorf("%w: advance in state %s", ErrInvalidState, r.state)
	}

	s.setClockLocked(at)
	r.progress += delta
	if r.progress >= r.in.Size {
		s.terminateLocked(r, StateCompleted, nil)
		s.scheduleLocked()
	}
	return nil
}

// Abort cancels a non-terminal request. Aborting a preload also aborts
// every attacher with cause ErrAborted.
func (s *Scheduler) Abort(id uint64, at int64) error {
	return s.stop(id, at, StateAborted, ErrAborted)
}

// Fail marks a non-terminal request as failed (e.g. network error).
func (s *Scheduler) Fail(id uint64, at int64) error {
	return s.stop(id, at, StateFailed, nil)
}

func (s *Scheduler) stop(id uint64, at int64, state State, cause error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == 0 {
		return fmt.Errorf("%w: id required", ErrInvalidArgument)
	}
	if at < s.now {
		return fmt.Errorf("%w: at=%d before now=%d", ErrClockSkew, at, s.now)
	}
	r, ok := s.reqs[id]
	if !ok {
		return fmt.Errorf("%w: id=%d", ErrNotFound, id)
	}
	if r.state.terminal() {
		return fmt.Errorf("%w: already %s", ErrInvalidState, r.state)
	}

	s.setClockLocked(at)
	s.terminateLocked(r, state, cause)
	s.scheduleLocked()
	return nil
}

// Tick advances the logical clock; a negative delta is clock skew.
func (s *Scheduler) Tick(delta int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if delta < 0 {
		return fmt.Errorf("%w: delta=%d", ErrClockSkew, delta)
	}
	s.setClockLocked(s.now + delta)
	return nil
}

// Status returns a consistent snapshot; terminal results never change.
func (s *Scheduler) Status(id uint64) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.reqs[id]
	if !ok {
		return Status{}, fmt.Errorf("%w: id=%d", ErrNotFound, id)
	}
	st := Status{
		State:             r.state,
		Progress:          r.progress,
		Size:              r.in.Size,
		Pauses:            r.pauses,
		EffectivePriority: r.effPriority(),
		FromCache:         r.fromCache,
		Cause:             r.cause,
	}
	if r.attachedTo != nil {
		st.AttachedTo = r.attachedTo.id
	}
	return st, nil
}

// Waste returns the preload waste report in expiry order.
func (s *Scheduler) Waste() []WasteEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]WasteEntry(nil), s.cache.waste...)
}

// Stats reports per-origin and total in-flight transfer counts.
func (s *Scheduler) Stats() (map[Origin]int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[Origin]int, len(s.byOrigin))
	for o, m := range s.byOrigin {
		out[o] = len(m)
	}
	return out, s.total
}

// CacheSize reports the number of live preload cache entries.
func (s *Scheduler) CacheSize() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cache.entries)
}

// fixOriginLocked refreshes heap membership for every tier of origin
// after deque or capacity changes.
func (s *Scheduler) fixOriginLocked(origin Origin) {
	spare := len(s.byOrigin[origin]) < s.cfg.PerOriginLimit
	for tier := 0; tier < numPriorities; tier++ {
		s.pending.fix(tier, origin, spare)
	}
}

// scheduleLocked starts requests while quotas allow. The next startable
// request (highest priority, then registration order, skipping origins at
// their connection limit) goes first; a queued highest-priority request
// that cannot start may preempt the lowest-priority, latest-started,
// still-pausable transfer of its origin.
func (s *Scheduler) scheduleLocked() {
	for {
		head := s.pending.peek()
		if head != nil && s.total < s.cfg.GlobalLimit {
			s.startLocked(head)
			continue
		}
		if top := s.pending.peekTop(); top != nil {
			originFull := len(s.byOrigin[top.in.Origin]) >= s.cfg.PerOriginLimit
			if originFull || s.total >= s.cfg.GlobalLimit {
				if v := s.findVictimLocked(top.in.Origin); v != nil {
					s.pauseLocked(v)
					continue
				}
			}
		}
		return
	}
}

// findVictimLocked picks the preemptable transfer of origin with the
// lowest effective priority; ties break to the latest start.
func (s *Scheduler) findVictimLocked(origin Origin) *request {
	var victim *request
	for _, r := range s.byOrigin[origin] {
		ep := r.effPriority()
		if ep >= PriorityHighest || r.pauses >= s.cfg.MaxPauses {
			continue
		}
		if victim == nil || ep < victim.effPriority() ||
			(ep == victim.effPriority() && r.startSeq > victim.startSeq) {
			victim = r
		}
	}
	return victim
}

func (s *Scheduler) startLocked(r *request) {
	s.pending.remove(r)
	r.state = StateTransferring
	s.startSeq++
	r.startSeq = s.startSeq
	m := s.byOrigin[r.in.Origin]
	if m == nil {
		m = make(map[uint64]*request)
		s.byOrigin[r.in.Origin] = m
	}
	m[r.id] = r
	s.total++
	s.fixOriginLocked(r.in.Origin)
}

// pauseLocked suspends a transfer; progress is kept, never rewound.
func (s *Scheduler) pauseLocked(r *request) {
	delete(s.byOrigin[r.in.Origin], r.id)
	s.total--
	r.pauses++
	r.state = StatePending
	s.pending.add(r, true)
	s.fixOriginLocked(r.in.Origin)
}

// terminateLocked drives a request to its final state and cascades the
// same fate to attachers. Quotas are released and cache updated.
func (s *Scheduler) terminateLocked(r *request, state State, cause error) {
	switch r.state {
	case StateTransferring:
		delete(s.byOrigin[r.in.Origin], r.id)
		s.total--
	case StatePending:
		s.pending.remove(r)
	case StateAttached:
		p := r.attachedTo
		for i, a := range p.attachers {
			if a == r {
				p.attachers = append(p.attachers[:i], p.attachers[i+1:]...)
				break
			}
		}
		r.attachedTo = nil
		s.pending.syncTier(p)
		s.fixOriginLocked(p.in.Origin)
	}
	r.state = state
	r.cause = cause
	s.fixOriginLocked(r.in.Origin)

	if r.in.Type == TypePreload {
		delete(s.activePreloads, cacheKey{r.in.Origin, r.in.URL})
		if state == StateCompleted {
			s.cache.put(cacheKey{r.in.Origin, r.in.URL}, &cacheEntry{
				as:          r.in.As,
				credentials: r.in.Credentials,
				integrity:   r.in.Integrity,
				completedAt: s.now,
			})
		}
	}
	for _, a := range r.attachers {
		a.attachedTo = nil
		a.state = state
		if state == StateCompleted {
			a.progress = a.in.Size
		}
		if state == StateAborted {
			a.cause = ErrAborted
		}
	}
	r.attachers = nil
}
