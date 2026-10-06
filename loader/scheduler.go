package loader

import "sync"

// Scheduler coordinates registration, quotas, preemption, the preload cache
// and completion notification. Every mutating method takes a logical
// timestamp; operations are serialized under one mutex, so concurrent
// callers observe a result equivalent to some serial order.
type Scheduler struct {
	cfg Config

	mu       sync.Mutex
	now      int64
	seq      uint64
	startSeq uint64
	nextID   uint64

	reqs    map[uint64]*Request
	pending *pendingQueue
	pendN   int

	inFlightByOrigin map[Origin]map[uint64]*Request
	inFlightPerOrig  map[Origin]int
	inFlightTotal    int

	preloads map[cacheKey]*Request
	cache    *preloadCache

	listeners []func(Event)

	// check, when set by tests, runs under the lock at the end of every
	// accepted mutating operation to assert invariants.
	check func()
}

// NewScheduler validates cfg and returns an empty scheduler.
func NewScheduler(cfg Config) (*Scheduler, error) {
	if cfg.PerOriginLimit <= 0 || cfg.GlobalLimit <= 0 {
		return nil, errInvalid("connection limits must be greater than zero")
	}
	if cfg.MaxPauses < 0 {
		return nil, errInvalid("max pauses must not be negative")
	}
	if cfg.PreloadTTL < 0 {
		return nil, errInvalid("preload TTL must not be negative")
	}
	return &Scheduler{
		cfg:              cfg,
		reqs:             make(map[uint64]*Request),
		pending:          newPendingQueue(),
		inFlightByOrigin: make(map[Origin]map[uint64]*Request),
		inFlightPerOrig:  make(map[Origin]int),
		preloads:         make(map[cacheKey]*Request),
		cache:            newPreloadCache(cfg.PreloadTTL),
	}, nil
}

// Subscribe registers a listener invoked with every event, in order, after
// the producing operation commits. Listeners must not call back into the
// scheduler.
func (s *Scheduler) Subscribe(l func(Event)) {
	s.mu.Lock()
	s.listeners = append(s.listeners, l)
	s.mu.Unlock()
}

// commit runs the invariant hook under the lock, unlocks, and dispatches
// events to subscribers.
func (s *Scheduler) commit(evs []Event) {
	if s.check != nil {
		s.check()
	}
	listeners := append([]func(Event){}, s.listeners...)
	s.mu.Unlock()
	for _, e := range evs {
		for _, l := range listeners {
			l(e)
		}
	}
}

// Query returns a snapshot of the request with the given id.
func (s *Scheduler) Query(id uint64) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reqs[id]
	if !ok {
		return Snapshot{}, errNotFound("unknown request id")
	}
	return r.snapshot(), nil
}

// Stats returns scheduler counters at the current logical time.
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	per := make(map[Origin]int, len(s.inFlightPerOrig))
	for o, n := range s.inFlightPerOrig {
		per[o] = n
	}
	return Stats{
		Now:               s.now,
		InFlightTotal:     s.inFlightTotal,
		InFlightPerOrigin: per,
		Pending:           s.pendN,
		CacheSize:         s.cache.size(),
		Wasted:            len(s.cache.wasted),
	}
}

// Wasted returns a copy of the waste report: preload cache entries that
// outlived their TTL without being used.
func (s *Scheduler) Wasted() []WasteRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]WasteRecord(nil), s.cache.wasted...)
}
