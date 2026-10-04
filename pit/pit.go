// Package pit implements point-in-time views on top of segstore, including
// open/renewal/expiry lifecycle, monotonic clock and global operation numbers.
package pit

import (
	"errors"
	"sort"
	"sync"

	"ontology/segstore"
)

var (
	// ErrInvalid is returned for malformed arguments.
	ErrInvalid = segstore.ErrInvalid
	// ErrClockRollback is returned when now is smaller than the accepted max.
	ErrClockRollback = errors.New("pit: clock rollback")
	// ErrPITNotFound is returned for unknown or already-expired PIT ids.
	ErrPITNotFound = errors.New("pit: point-in-time not found")
	// ErrPITLimit is returned from Open when the surviving PIT count hits Pmax.
	ErrPITLimit = errors.New("pit: point-in-time limit exceeded")
)

// State errors produced by the underlying store, re-exported for callers.
var (
	ErrIDConflict  = segstore.ErrIDConflict
	ErrDocNotFound = segstore.ErrDocNotFound
	ErrSegNotFound = segstore.ErrSegNotFound
)

// PIT describes an open point-in-time view.
type PIT struct {
	ID   int
	Op   int64
	Exp  int64
	Segs []int // sorted segment set captured at Open
}

type pitState struct {
	id   int
	op   int64
	exp  int64
	segs []int
}

// Manager is the composition root serializing every operation under one lock.
type Manager struct {
	mu      sync.Mutex
	store   *segstore.Store
	pmax    int
	now     int64
	nextOp  int64
	nextPIT int
	pits    map[int]*pitState

	// Touched counts segment records touched by Open and Close only; it is
	// independent of document counts.
	Touched int64
}

// NewManager creates a Manager with PIT capacity pmax (1..1000).
func NewManager(pmax int) *Manager {
	return &Manager{
		store:   segstore.New(),
		pmax:    pmax,
		nextOp:  1,
		nextPIT: 1,
		pits:    map[int]*pitState{},
	}
}

// Store exposes the underlying store (used by search).
func (m *Manager) Store() *segstore.Store { return m.store }

// Now returns the currently accepted clock value.
func (m *Manager) Now() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

// PITs returns a snapshot of the currently open point-in-time views.
func (m *Manager) PITs() []PIT {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PIT, 0, len(m.pits))
	for _, p := range m.pits {
		out = append(out, PIT{ID: p.id, Op: p.op, Exp: p.exp, Segs: append([]int(nil), p.segs...)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

// begin performs parameter and clock checks for a now-bearing operation,
// advances the clock, and lands all expired PITs before any other decision.
func (m *Manager) begin(now int64) error {
	if !validNow(now) {
		return ErrInvalid
	}
	if now < m.now {
		return ErrClockRollback
	}
	m.now = now
	m.landExpiredLocked()
	return nil
}

// landExpiredLocked lands every PIT with exp <= current clock.
func (m *Manager) landExpiredLocked() {
	var expired []int
	for id, p := range m.pits {
		if p.exp <= m.now {
			expired = append(expired, id)
		}
	}
	sort.Ints(expired)
	var released []int
	for _, id := range expired {
		p := m.pits[id]
		released = append(released, p.segs...)
		m.Touched += int64(len(p.segs))
		delete(m.pits, id)
	}
	m.releaseLocked(released)
}

// releaseLocked drops references for the batch and physically frees, in
// ascending segment order, those that lost their last reference.
func (m *Manager) releaseLocked(segIDs []int) {
	for _, sid := range segIDs {
		m.store.ReleaseLocked(sid)
	}
	m.store.FlushReleasedLocked(segIDs)
}

func (m *Manager) allocOp() int64 {
	op := m.nextOp
	m.nextOp++
	return op
}

// AddSegment creates a new segment and consumes one operation number.
func (m *Manager) AddSegment(now int64, docs []segstore.Doc) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(now); err != nil {
		return 0, err
	}
	if err := m.store.CheckAddLocked(docs); err != nil {
		return 0, err
	}
	m.allocOp()
	return m.store.AddSegmentLocked(docs)
}

// Delete tombstones a live document and consumes one operation number.
func (m *Manager) Delete(now int64, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(now); err != nil {
		return err
	}
	if id == "" {
		return ErrInvalid
	}
	if err := m.store.CheckDeleteLocked(id); err != nil {
		return err
	}
	op := m.allocOp()
	m.store.DeleteLocked(id, op)
	return nil
}

// Merge merges view segments and consumes one operation number.
func (m *Manager) Merge(now int64, segIDs []int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(now); err != nil {
		return 0, err
	}
	if len(segIDs) < 2 || len(segIDs) > 10 {
		return 0, ErrInvalid
	}
	if err := m.store.CheckMergeLocked(segIDs); err != nil {
		return 0, err
	}
	m.allocOp()
	return m.store.MergeLocked(segIDs)
}

// Open captures the current view and consumes one operation number.
func (m *Manager) Open(now int64, ka int64) (PIT, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validNow(now) {
		return PIT{}, ErrInvalid
	}
	if ka < 1 || ka > 1_000_000_000 {
		return PIT{}, ErrInvalid
	}
	if now < m.now {
		return PIT{}, ErrClockRollback
	}
	m.now = now
	m.landExpiredLocked()
	if len(m.pits) >= m.pmax {
		return PIT{}, ErrPITLimit
	}
	op := m.allocOp()
	segs := m.store.ViewLocked()
	for _, sid := range segs {
		m.store.AcquireLocked(sid)
	}
	m.Touched += int64(len(segs))
	id := m.nextPIT
	m.nextPIT++
	p := &pitState{id: id, op: op, exp: now + ka, segs: segs}
	m.pits[id] = p
	return PIT{ID: id, Op: op, Exp: p.exp, Segs: append([]int(nil), segs...)}, nil
}

// Close lands a PIT immediately.
func (m *Manager) Close(now int64, pid int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.begin(now); err != nil {
		return err
	}
	if pid <= 0 {
		return ErrInvalid
	}
	p, ok := m.pits[pid]
	if !ok {
		return ErrPITNotFound
	}
	m.Touched += int64(len(p.segs))
	segs := p.segs
	delete(m.pits, pid)
	m.releaseLocked(segs)
	return nil
}

// usePIT validates a PIT for a search and returns its state.
func (m *Manager) usePIT(now int64, pid int) (*pitState, error) {
	if pid <= 0 {
		return nil, ErrInvalid
	}
	p, ok := m.pits[pid]
	if !ok {
		return nil, ErrPITNotFound
	}
	return p, nil
}

// Search pages over pid (0 = current view) after key with the given page size.
// When pid != 0 and ka > 0 the expiry is extended to max(exp, now+ka).
func (m *Manager) Search(now int64, pid, size int, after *segstore.Key, ka int64) ([]segstore.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if size < 1 || size > 1000 {
		return nil, ErrInvalid
	}
	if ka < 0 || ka > 1_000_000_000 {
		return nil, ErrInvalid
	}
	if pid == 0 {
		if after != nil || ka != 0 {
			return nil, ErrInvalid
		}
	}
	if err := m.begin(now); err != nil {
		return nil, err
	}

	if pid == 0 {
		segs := m.store.ViewLocked()
		// Current view: every tombstone belongs to a past operation, so an
		// op larger than any allocated one hides all deleted documents.
		return m.store.ScanLocked(segs, m.nextOp, nil, size), nil
	}

	p, err := m.usePIT(now, pid)
	if err != nil {
		return nil, err
	}
	if ka > 0 {
		if cand := now + ka; cand > p.exp {
			p.exp = cand
		}
	}
	return m.store.ScanLocked(append([]int(nil), p.segs...), p.op, after, size), nil
}

// Released returns a copy of the physical release log.
func (m *Manager) Released() []int { return m.store.Released() }
