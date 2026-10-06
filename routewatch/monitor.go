package routewatch

import (
	"sort"
	"sync"
)

// Monitor is the concurrency-safe entry point. All operations are serialized
// by a single mutex, which makes "equivalent to some serial order" a direct
// consequence of execution rather than a best-effort guarantee.
type Monitor struct {
	mu       sync.Mutex
	cfg      *Config
	clock    *clock
	engine   *engine
	pub      *publisher
	results  []StopResult
	reported map[int]bool
	anchors  map[int]int64
}

func New(cfg Config) (*Monitor, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	c := cfg
	// Defensive copies: CancelStop mutates the stop slice and later callers
	// must be able to reuse the same Config value to build independent
	// monitors.
	c.Stops = append([]Stop(nil), cfg.Stops...)
	durations := make(map[[2]string]int64, len(cfg.Travel.Duration))
	for k, v := range cfg.Travel.Duration {
		durations[k] = v
	}
	c.Travel.Duration = durations
	m := &Monitor{
		cfg:      &c,
		clock:    newClock(c.Departure),
		engine:   newEngine(&c),
		results:  make([]StopResult, len(c.Stops)),
		reported: map[int]bool{},
		anchors:  map[int]int64{},
	}
	m.pub = newPublisher(c.DebounceSeconds, c.LockWindow, len(c.Stops))
	m.engine.resimulate(m.results, 0, m.anchors)
	m.pub.republish(m.results, m.reported, m.clock.last, 0)
	return m, nil
}

// ReportArrival records the actual arrival of a stop. On acceptance the stop
// and every stop after it are re-simulated with the new information.
func (m *Monitor) ReportArrival(stopID string, actualArrival int64, opTime int64) (*Snapshot, error) {
	if stopID == "" || actualArrival < 0 || opTime < 0 {
		return nil, ErrInvalidParam
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if opTime < m.clock.last {
		return nil, ErrClockRollback
	}
	idx, ok := m.indexOf(stopID)
	if !ok {
		return nil, ErrStopNotFound
	}
	switch {
	case m.cfg.Stops[idx].Canceled:
		return nil, ErrInvalidState
	case m.reported[idx]:
		return nil, ErrInvalidState
	case !m.results[idx].Valid:
		return nil, ErrStopSkipped
	}
	// Ordering: the actual arrival must be strictly later than the departure
	// of the preceding actually arrived stop (the depot before the first
	// report). The check rejects the operation before any state mutation.
	prevDeparture := m.cfg.Departure
	for j := idx - 1; j >= 0; j-- {
		if m.reported[j] && m.results[j].Valid {
			prevDeparture = m.results[j].Departure
			break
		}
	}
	if actualArrival <= prevDeparture {
		return nil, ErrOutOfOrder
	}

	if err := m.clock.accept(opTime); err != nil {
		return nil, err
	}
	m.reported[idx] = true
	m.anchors[idx] = actualArrival
	m.engine.resimulate(m.results, idx, m.anchors)
	m.pub.republish(m.results, m.reported, m.clock.last, idx)
	sn := m.snapshotLocked()
	return &sn, nil
}

// CancelStop removes a stop that has no reported arrival. Later stops are
// re-simulated with the canceled point skipped.
func (m *Monitor) CancelStop(stopID string, opTime int64) (*Snapshot, error) {
	if stopID == "" || opTime < 0 {
		return nil, ErrInvalidParam
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if opTime < m.clock.last {
		return nil, ErrClockRollback
	}
	idx, ok := m.indexOf(stopID)
	if !ok {
		return nil, ErrStopNotFound
	}
	// Already arrived, already skipped (hard window) or already canceled.
	if m.cfg.Stops[idx].Canceled || m.reported[idx] || !m.results[idx].Valid {
		return nil, ErrInvalidState
	}

	if err := m.clock.accept(opTime); err != nil {
		return nil, err
	}
	m.cfg.Stops[idx].Canceled = true
	delete(m.anchors, idx)
	m.engine.resimulate(m.results, idx, m.anchors)
	m.pub.republish(m.results, m.reported, m.clock.last, idx)
	sn := m.snapshotLocked()
	return &sn, nil
}

// Snapshot returns a defensive copy of the current results and publications.
func (m *Monitor) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Monitor) indexOf(id string) (int, bool) {
	for i := range m.cfg.Stops {
		if m.cfg.Stops[i].ID == id {
			return i, true
		}
	}
	return 0, false
}

func (m *Monitor) snapshotLocked() Snapshot {
	results := append([]StopResult(nil), m.results...)
	published := m.pub.list(m.results)
	sort.SliceStable(published, func(i, j int) bool { return published[i].Index < published[j].Index })
	return Snapshot{
		RouteID:   m.cfg.RouteID,
		Clock:     m.clock.last,
		Results:   results,
		Published: published,
	}
}
