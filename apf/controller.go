package apf

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Controller is the admission controller. All methods are safe for
// concurrent use; results are always equivalent to some serial order because
// every operation runs under a single mutex.
//
// Time is injected by the caller and must never be earlier than the time of
// a previously accepted operation. Rejected operations change no admission
// state of their own (no seat, queue or round-robin effect) and do not
// advance the clock; time-driven maintenance (timeout eviction and the
// dispatch it unblocks) is applied lazily at the start of every operation
// whose arguments and clock are valid.
type Controller struct {
	mu sync.Mutex

	cfg     Config
	rules   []compiledRule
	levels  map[string]*Level      // by name, from the current config
	nominal map[string]int         // limited levels only
	states  map[string]*levelState // limited levels with activity

	lastTime time.Time
	seq      uint64
	probe    uint64
}

// NewController validates cfg and returns a controller whose clock starts
// at t.
func NewController(t time.Time, cfg Config) (*Controller, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	c := &Controller{
		lastTime: t,
		states:   make(map[string]*levelState),
	}
	c.applyConfig(cfg)
	return c, nil
}

// applyConfig installs cfg as the current configuration, recomputing
// nominal seats and reconciling level states. Caller holds the lock.
func (c *Controller) applyConfig(cfg Config) {
	c.cfg = cfg
	c.rules = compileRules(cfg.Rules)
	c.levels = make(map[string]*Level, len(cfg.Levels))
	for i := range cfg.Levels {
		l := &cfg.Levels[i]
		c.levels[l.Name] = l
	}
	c.nominal = allocateNominalSeats(cfg.Levels, cfg.TotalSeats)
	for name, n := range c.nominal {
		l := c.levels[name]
		st, ok := c.states[name]
		if !ok {
			st = newLevelState(name, n, l.QueueLimit, l.QueueTimeout, &c.probe)
			c.states[name] = st
			continue
		}
		st.nominal = n
		st.queueLimit = l.QueueLimit
		st.queueTimeout = l.QueueTimeout
	}
	// Levels that disappeared from the config or became exempt keep their
	// state (in-flight requests are not interrupted, queued requests keep
	// queueing) but get zero nominal seats.
	for name, st := range c.states {
		if _, ok := c.nominal[name]; !ok {
			st.nominal = 0
			st.queueLimit = 0
		}
	}
}

// maintenance applies time-driven transitions at t: evict timed-out waiters
// and dispatch whatever the evictions unblocked. Caller holds the lock.
func (c *Controller) maintenance(t time.Time) {
	for _, st := range c.states {
		for _, w := range st.evict(t) {
			w.ticket.resolve(Result{Err: errf(KindQueueTimeout,
				fmt.Sprintf("level %q: waited >= %s", st.name, st.queueTimeout))})
		}
	}
	c.dispatchAll()
}

// dispatchAll dispatches every level and notifies granted waiters.
// Caller holds the lock.
func (c *Controller) dispatchAll() {
	for _, st := range c.states {
		for _, w := range st.dispatch() {
			w.ticket.resolve(Result{Lease: &Lease{ctrl: c, level: st, seats: w.req.Seats}})
		}
	}
}

// checkClock rejects operations earlier than the last accepted one.
func (c *Controller) checkClock(t time.Time) error {
	if t.Before(c.lastTime) {
		return errf(KindClockSkew, fmt.Sprintf("time %s is before last accepted operation at %s", t, c.lastTime))
	}
	return nil
}

func validateRequest(req Request) error {
	if req.Seats < 1 || req.Seats > 10 {
		return errf(KindInvalidArgument, fmt.Sprintf("seats %d outside [1, 10]", req.Seats))
	}
	return nil
}

// Admit classifies req and either executes it immediately, queues it, or
// rejects it.
func (c *Controller) Admit(t time.Time, req Request) (*AdmitResult, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(t); err != nil {
		return nil, err
	}
	c.maintenance(t)

	levelName, flowKey, ok := classify(c.rules, req)
	if !ok {
		return nil, errf(KindNoMatch, fmt.Sprintf("no rule matches user=%q verb=%q resource=%q", req.User, req.Verb, req.Resource))
	}
	level := c.levels[levelName]
	if level.Exempt {
		c.lastTime = t
		return &AdmitResult{Lease: &Lease{ctrl: c}}, nil
	}
	st := c.states[levelName]
	if req.Seats > st.nominal {
		return nil, errf(KindUnsatisfiable,
			fmt.Sprintf("level %q: seats %d exceed nominal %d", levelName, req.Seats, st.nominal))
	}
	if st.waiters == 0 && st.occupied+req.Seats <= st.nominal {
		st.occupied += req.Seats
		c.lastTime = t
		return &AdmitResult{Lease: &Lease{ctrl: c, level: st, seats: req.Seats}}, nil
	}
	if st.waiters >= st.queueLimit {
		return nil, errf(KindQueueFull,
			fmt.Sprintf("level %q: %d waiters reach limit %d", levelName, st.waiters, st.queueLimit))
	}
	c.seq++
	w := &waiter{
		req:        req,
		enqueuedAt: t,
		deadline:   t.Add(st.queueTimeout),
		seq:        c.seq,
		flow:       &flowState{key: flowKey},
		level:      st,
		ticket:     &Ticket{ch: make(chan Result, 1)},
	}
	st.enqueue(w)
	c.lastTime = t
	return &AdmitResult{Ticket: w.ticket}, nil
}

// Finish releases the lease's seats and dispatches queued requests in the
// same operation. Finishing twice is an invalid-argument error.
func (l *Lease) Finish(t time.Time) error {
	c := l.ctrl
	c.mu.Lock()
	defer c.mu.Unlock()
	if l.done {
		return errf(KindInvalidArgument, "lease already finished")
	}
	if err := c.checkClock(t); err != nil {
		return err
	}
	c.maintenance(t)
	l.done = true
	if l.level != nil {
		l.level.occupied -= l.seats
	}
	c.dispatchAll()
	c.lastTime = t
	return nil
}

// UpdateConfig atomically replaces the whole configuration. In-flight
// requests are not interrupted or reclassified; queued requests keep
// queueing in the level they were classified into but become subject to the
// new nominal seats; queued requests that can never fit under the new
// nominal seats are rejected immediately. If cfg is invalid nothing changes.
func (c *Controller) UpdateConfig(t time.Time, cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(t); err != nil {
		return err
	}
	c.maintenance(t)
	c.applyConfig(cfg)
	// Reject queued requests that the new nominal seats make unsatisfiable.
	for _, st := range c.states {
		if st.waiters == 0 {
			continue
		}
		var rejected []*waiter
		for el := st.flowOrder.Front(); el != nil; {
			nextFlow := el.Next()
			fs := el.Value.(*flowState)
			for qe := fs.queue.Front(); qe != nil; {
				next := qe.Next()
				w := qe.Value.(*waiter)
				if w.req.Seats > st.nominal {
					st.unlink(w, qe)
					rejected = append(rejected, w)
				}
				qe = next
			}
			el = nextFlow
		}
		for _, w := range rejected {
			w.ticket.resolve(Result{Err: errf(KindUnsatisfiable,
				fmt.Sprintf("level %q: seats %d exceed new nominal %d", st.name, w.req.Seats, st.nominal))})
		}
	}
	c.dispatchAll()
	// Prune states that are idle and no longer in the configuration.
	for name, st := range c.states {
		if _, ok := c.nominal[name]; !ok && st.occupied == 0 && st.waiters == 0 {
			delete(c.states, name)
		}
	}
	c.lastTime = t
	return nil
}

// LevelDebug exposes a level's observable state for tests and monitoring.
type LevelDebug struct {
	Name     string
	Nominal  int
	Occupied int
	Waiters  int
	Flows    []string
}

// DebugState returns a deterministic snapshot of the controller.
func (c *Controller) DebugState() (lastTime time.Time, levels []LevelDebug) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lastTime = c.lastTime
	names := make([]string, 0, len(c.states))
	for name := range c.states {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		st := c.states[name]
		ld := LevelDebug{
			Name:     name,
			Nominal:  st.nominal,
			Occupied: st.occupied,
			Waiters:  st.waiters,
		}
		for el := st.flowOrder.Front(); el != nil; el = el.Next() {
			ld.Flows = append(ld.Flows, el.Value.(*flowState).key)
		}
		levels = append(levels, ld)
	}
	return lastTime, levels
}

// ProbeCount returns the number of key comparisons performed by the timeout
// heaps. It lets tests verify that enqueue/dequeue/timeout costs grow
// logarithmically, not linearly, with the number of waiters, and never
// depend on the number of flows.
func (c *Controller) ProbeCount() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.probe
}
