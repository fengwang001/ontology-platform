// Package restart provides the tunnel label manager: it binds forwarding
// equivalence classes to labels with per-client owners, handles graceful
// client restarts via stale ownerships, and rebuilds its state from a
// label-level ALLOC/FREE log after its own restart.
//
// Expiration is a pure function of time: every accepted operation first
// lands stale/placeholder removals whose deadline is at or before now, in
// (deadline, fec) order, and the resulting FREE events carry the deadline
// as their timestamp. Rejected operations change no state, land no
// expirations, append no log entries, and do not advance the clock.
package restart

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/binding"
	"ontology/labelpool"
)

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrInvalidParam    = errors.New("restart: invalid parameter")
	ErrClockBackwards  = errors.New("restart: clock moved backwards")
	ErrClientOffline   = errors.New("restart: client is offline")
	ErrStateMismatch   = errors.New("restart: client state mismatch")
	ErrBindingNotFound = errors.New("restart: binding not found")
	ErrOwnerLimit      = errors.New("restart: per-client owner limit reached")
	ErrNoLabel         = labelpool.ErrExhausted
)

const (
	minLo     = 16
	maxLabel  = 1048575
	maxPeriod = 1000000000 // Hd and R are at most 1e9 ms
	minQ      = 1
	maxQ      = 1000000
	maxClient = 10000
	maxTime   = 1000000000000 // now is at most 1e12 ms
)

// LogOp identifies a label-layer log event.
type LogOp int

const (
	// LogAlloc records that fec was bound to label at the event time.
	LogAlloc LogOp = iota + 1
	// LogFree records that fec's binding to label was released.
	LogFree
)

// LogEvent is one label-layer journal entry.
type LogEvent struct {
	Op    LogOp
	Fec   string
	Label int
	Time  uint64
}

// freeRec remembers a fec's most recently released label for affinity.
type freeRec struct {
	label int
	at    uint64
}

// expEntry is one pending expiration: a stale ownership or a placeholder.
type expEntry struct {
	deadline uint64
	fec      string
	client   int // binding.PlaceholderID for placeholders
}

// expHeap is a min-heap ordered by (deadline, fec, client). Entries are
// removed lazily: validity is re-checked against the binding table.
type expHeap []expEntry

func (h expHeap) Len() int { return len(h) }

func (h expHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.deadline != b.deadline {
		return a.deadline < b.deadline
	}
	if a.fec != b.fec {
		return a.fec < b.fec
	}
	return a.client < b.client
}

func (h expHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *expHeap) Push(x any) { *h = append(*h, x.(expEntry)) }

func (h *expHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Manager binds fecs to labels and journals label-layer events. All methods
// are safe for concurrent use; results equal some serial order.
type Manager struct {
	mu       sync.Mutex
	pool     *labelpool.Pool
	table    *binding.Table
	lo, hi   int
	q        int
	hd, r    uint64
	maxNow   uint64
	log      []LogEvent
	lastFree map[string]freeRec
	exp      expHeap

	// probes: statistics of the most recent accepted Bind.
	probeExamined int // labels examined while picking the minimum available
	probeExpiries int // expirations landed by that Bind (sweep + isolation)
}

// New creates a manager for labels [lo, hi] with hold-down hd, restart
// recovery window r, and per-client ownership limit q.
func New(lo, hi int, hd, r uint64, q int) (*Manager, error) {
	if lo < minLo || hi > maxLabel || lo > hi ||
		hd > maxPeriod || r > maxPeriod || q < minQ || q > maxQ {
		return nil, ErrInvalidParam
	}
	m := &Manager{lo: lo, hi: hi, hd: hd, r: r, q: q}
	m.pool = labelpool.NewPool(lo, hi, hd)
	m.table = binding.NewTable(q)
	m.lastFree = make(map[string]freeRec)
	return m, nil
}

// checkFecClient validates fec and client ranges shared by all operations.
func checkFecClient(fec string, client int, now uint64) error {
	if len(fec) == 0 || len(fec) > 64 || client < 1 || client > maxClient || now > maxTime {
		return ErrInvalidParam
	}
	return nil
}

// checkClient validates client and now for client-scoped operations.
func checkClient(client int, now uint64) error {
	if client < 1 || client > maxClient || now > maxTime {
		return ErrInvalidParam
	}
	return nil
}

// expiryValid reports whether a heap entry still matches a live stale or
// placeholder ownership.
func (m *Manager) expiryValid(e expEntry) bool {
	b, ok := m.table.Get(e.fec)
	if !ok {
		return false
	}
	o := b.Owners[e.client]
	if o == nil || o.Deadline != e.deadline {
		return false
	}
	if e.client == binding.PlaceholderID {
		return o.Placeholder
	}
	return o.Stale && !o.Placeholder
}

// collectExpired pops every valid expiration with deadline <= now from the
// heap without applying its effects. Invalid bookkeeping entries are
// discarded. The caller must later either apply or restore the entries.
func (m *Manager) collectExpired(now uint64) []expEntry {
	var out []expEntry
	for len(m.exp) > 0 && m.exp[0].deadline <= now {
		e := heap.Pop(&m.exp).(expEntry)
		if m.expiryValid(e) {
			out = append(out, e)
		}
	}
	return out
}

// restoreExpired pushes collected entries back; used when an operation is
// rejected so that no expiration is landed.
func (m *Manager) restoreExpired(entries []expEntry) {
	for _, e := range entries {
		heap.Push(&m.exp, e)
	}
}

// applyExpired lands collected expirations in (deadline, fec) order: owners
// are removed and bindings whose last owner leaves are freed at the
// expiration deadline, not at the current time.
func (m *Manager) applyExpired(entries []expEntry) {
	for _, e := range entries {
		if !m.expiryValid(e) { // duplicate heap entries are possible
			continue
		}
		label, emptied := m.table.RemoveOwner(e.fec, e.client)
		if emptied {
			m.pool.Free(label, e.deadline)
			m.appendLog(LogEvent{Op: LogFree, Fec: e.fec, Label: label, Time: e.deadline})
			m.lastFree[e.fec] = freeRec{label: label, at: e.deadline}
		}
	}
}

// sweepFrees computes which fecs the collected expirations would release
// entirely, mapping fec to the FREE moment (the latest expiring owner's
// deadline).
func (m *Manager) sweepFrees(entries []expEntry) map[string]uint64 {
	type agg struct {
		owners map[int]bool
		max    uint64
	}
	per := make(map[string]*agg)
	for _, e := range entries {
		a := per[e.fec]
		if a == nil {
			a = &agg{owners: make(map[int]bool)}
			per[e.fec] = a
		}
		a.owners[e.client] = true
		if e.deadline > a.max {
			a.max = e.deadline
		}
	}
	frees := make(map[string]uint64)
	for fec, a := range per {
		if b, ok := m.table.Get(fec); ok && len(b.Owners) == len(a.owners) {
			frees[fec] = a.max
		}
	}
	return frees
}

func (m *Manager) appendLog(ev LogEvent) {
	m.log = append(m.log, ev)
}

// Bind returns the label for fec, adding client as an owner. Rejection
// order: invalid parameter, clock backwards, client offline, then (in
// order) idempotent success, owner limit, label exhaustion.
func (m *Manager) Bind(fec string, client int, now uint64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := checkFecClient(fec, client, now); err != nil {
		return 0, err
	}
	if now < m.maxNow {
		return 0, ErrClockBackwards
	}
	if m.table.State(client) == binding.StateOffline {
		return 0, ErrClientOffline
	}
	expired := m.collectExpired(now)
	apply, err := m.decideBind(fec, client, now, m.sweepFrees(expired), expiredCounts(expired))
	if err != nil {
		m.restoreExpired(expired)
		return 0, err
	}
	m.applyExpired(expired)
	m.probeExamined, m.probeExpiries = 0, len(expired)
	label := apply()
	m.maxNow = now
	return label, nil
}

// expiredCounts tallies how many distinct ownerships each client loses in
// the collected expirations.
func expiredCounts(entries []expEntry) map[int]int {
	type key struct {
		fec    string
		client int
	}
	seen := make(map[key]bool)
	counts := make(map[int]int)
	for _, e := range entries {
		k := key{e.fec, e.client}
		if !seen[k] {
			seen[k] = true
			counts[e.client]++
		}
	}
	return counts
}

// decideBind computes the outcome of an accepted Bind against the state as
// it would be after the pending expirations land, without mutating anything.
// On success it returns the closure that performs the mutation.
func (m *Manager) decideBind(fec string, client int, now uint64, frees map[string]uint64, expCounts map[int]int) (func() int, error) {
	effCount := -1
	underLimit := func() bool {
		if effCount < 0 {
			effCount = m.table.Count(client) - expCounts[client]
		}
		return effCount < m.table.Q
	}
	if b, ok := m.table.Get(fec); ok {
		if o := b.Owners[client]; o != nil && !(o.Stale && o.Deadline <= now) {
			// Already a normal owner (idempotent) or a live stale owner
			// (refresh): succeeds regardless of Q and label availability.
			return func() int {
				if o.Stale {
					m.table.Refresh(fec, client)
				}
				return b.Label
			}, nil
		}
		remaining, hasPlaceholder := 0, false
		for id, o := range b.Owners {
			if (o.Stale || o.Placeholder) && o.Deadline <= now {
				continue // expires in this operation's sweep
			}
			remaining++
			if id == binding.PlaceholderID && o.Placeholder {
				hasPlaceholder = true
			}
		}
		if remaining > 0 {
			// Join an existing binding; a placeholder is claimed.
			if !underLimit() {
				return nil, ErrOwnerLimit
			}
			return func() int {
				m.table.AddOwner(fec, client)
				if hasPlaceholder {
					m.table.RemoveOwner(fec, binding.PlaceholderID)
				}
				return b.Label
			}, nil
		}
		// The binding's last owners expire in this sweep: the fec is
		// re-bound below, with affinity to the just-released label.
		if !underLimit() {
			return nil, ErrOwnerLimit
		}
		return m.decideAlloc(fec, client, now, freeRec{label: b.Label, at: frees[fec]}, true, frees)
	}
	if !underLimit() {
		return nil, ErrOwnerLimit
	}
	fr, hasAffinity := m.lastFree[fec]
	return m.decideAlloc(fec, client, now, fr, hasAffinity, frees)
}

// decideAlloc decides a fresh binding for an unbound fec: affinity retake of
// the last released label while it is still isolated, otherwise the smallest
// available label.
func (m *Manager) decideAlloc(fec string, client int, now uint64, fr freeRec, hasAffinity bool, frees map[string]uint64) (func() int, error) {
	if hasAffinity && now < fr.at+m.hd {
		return func() int {
			if m.pool.AllocateSpecific(fr.label) {
				m.table.AddBinding(fec, fr.label, client)
				m.appendLog(LogEvent{Op: LogAlloc, Fec: fec, Label: fr.label, Time: now})
				return fr.label
			}
			// Unreachable for consistent state; fall back to the pool.
			return m.allocMin(fec, client, now)
		}, nil
	}
	available := m.pool.WouldAvailable(now)
	if !available {
		for _, at := range frees {
			if at+m.hd <= now {
				available = true
				break
			}
		}
	}
	if !available {
		return nil, ErrNoLabel
	}
	return func() int { return m.allocMin(fec, client, now) }, nil
}

// allocMin performs the actual minimum-label allocation and logging.
func (m *Manager) allocMin(fec string, client int, now uint64) int {
	label, ok := m.pool.AllocateMin(now)
	if !ok {
		// Availability was checked during the decision phase.
		panic("restart: label pool unexpectedly exhausted")
	}
	m.table.AddBinding(fec, label, client)
	m.appendLog(LogEvent{Op: LogAlloc, Fec: fec, Label: label, Time: now})
	m.probeExamined = m.pool.Examined()
	m.probeExpiries += m.pool.Promotions()
	return label
}

// Unbind removes client's ownership of fec; the label is released when the
// last owner leaves. Rejection order: invalid parameter, clock backwards,
// client offline, binding not found.
func (m *Manager) Unbind(fec string, client int, now uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := checkFecClient(fec, client, now); err != nil {
		return err
	}
	if now < m.maxNow {
		return ErrClockBackwards
	}
	if m.table.State(client) == binding.StateOffline {
		return ErrClientOffline
	}
	expired := m.collectExpired(now)
	var o *binding.Owner
	if b, ok := m.table.Get(fec); ok {
		o = b.Owners[client]
	}
	if o == nil || (o.Stale && o.Deadline <= now) {
		m.restoreExpired(expired)
		return ErrBindingNotFound
	}
	m.applyExpired(expired)
	m.releaseOwner(fec, client, now)
	m.maxNow = now
	return nil
}

// releaseOwner removes one owner and frees the label at freeTime when the
// binding empties.
func (m *Manager) releaseOwner(fec string, client int, freeTime uint64) {
	label, emptied := m.table.RemoveOwner(fec, client)
	if emptied {
		m.pool.Free(label, freeTime)
		m.appendLog(LogEvent{Op: LogFree, Fec: fec, Label: label, Time: freeTime})
		m.lastFree[fec] = freeRec{label: label, at: freeTime}
	}
}

// ClientDown marks every normal ownership of client stale with deadline
// now+R (already stale ones keep their deadline) and takes the client
// offline.
func (m *Manager) ClientDown(client int, now uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := checkClient(client, now); err != nil {
		return err
	}
	if now < m.maxNow {
		return ErrClockBackwards
	}
	if m.table.State(client) == binding.StateOffline {
		return ErrStateMismatch
	}
	expired := m.collectExpired(now)
	m.applyExpired(expired)
	for _, fec := range m.table.MarkStale(client, now+m.r) {
		heap.Push(&m.exp, expEntry{deadline: now + m.r, fec: fec, client: client})
	}
	m.table.SetState(client, binding.StateOffline)
	m.maxNow = now
	return nil
}

// ClientUp moves an offline client into the recovering state.
func (m *Manager) ClientUp(client int, now uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := checkClient(client, now); err != nil {
		return err
	}
	if now < m.maxNow {
		return ErrClockBackwards
	}
	if m.table.State(client) != binding.StateOffline {
		return ErrStateMismatch
	}
	expired := m.collectExpired(now)
	m.applyExpired(expired)
	m.table.SetState(client, binding.StateRecovering)
	m.maxNow = now
	return nil
}

// EndOfRib drops every still stale ownership of a recovering client and
// returns it to the normal state. Rejection order: invalid parameter,
// clock backwards, client offline, state mismatch.
func (m *Manager) EndOfRib(client int, now uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := checkClient(client, now); err != nil {
		return err
	}
	if now < m.maxNow {
		return ErrClockBackwards
	}
	switch m.table.State(client) {
	case binding.StateOffline:
		return ErrClientOffline
	case binding.StateRecovering:
	default:
		return ErrStateMismatch
	}
	expired := m.collectExpired(now)
	m.applyExpired(expired)
	fecs := m.table.StaleFecs(client)
	sort.Strings(fecs) // deterministic FREE order
	for _, fec := range fecs {
		m.releaseOwner(fec, client, now)
	}
	m.table.SetState(client, binding.StateNormal)
	m.maxNow = now
	return nil
}

// Restore rebuilds the manager from a label-level log: every fec still
// bound at the end of the log gets a placeholder owner expiring at now+R,
// and all clients return to the normal state. Any prefix of a log the
// manager produced is valid. now must be at least the greatest event time
// in the log and at least the greatest previously accepted now.
func (m *Manager) Restore(log []LogEvent, now uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if now > maxTime {
		return ErrInvalidParam
	}
	fresh, maxLog, err := m.replay(log)
	if err != nil {
		return err
	}
	if now < maxLog || now < m.maxNow {
		return ErrClockBackwards
	}
	for fec := range fresh.table.Bindings() {
		fresh.table.AddPlaceholder(fec, now+m.r)
		heap.Push(&fresh.exp, expEntry{deadline: now + m.r, fec: fec, client: binding.PlaceholderID})
	}
	fresh.log = append([]LogEvent(nil), log...)
	fresh.maxNow = now
	m.pool, m.table, m.lastFree, m.exp = fresh.pool, fresh.table, fresh.lastFree, fresh.exp
	m.log, m.maxNow = fresh.log, fresh.maxNow
	m.probeExamined, m.probeExpiries = 0, 0
	return nil
}

// replay validates the log and rebuilds pool, table, and affinity state.
func (m *Manager) replay(log []LogEvent) (*Manager, uint64, error) {
	fresh := &Manager{
		lo:       m.lo,
		hi:       m.hi,
		hd:       m.hd,
		r:        m.r,
		pool:     labelpool.NewPool(m.lo, m.hi, m.hd),
		table:    binding.NewTable(m.q),
		lastFree: make(map[string]freeRec),
	}
	var maxLog uint64
	for i, ev := range log {
		if ev.Op != LogAlloc && ev.Op != LogFree ||
			len(ev.Fec) == 0 || len(ev.Fec) > 64 ||
			ev.Label < m.lo || ev.Label > m.hi || ev.Time > maxTime {
			return nil, 0, fmt.Errorf("%w: log entry %d", ErrInvalidParam, i)
		}
		if ev.Time > maxLog {
			maxLog = ev.Time
		}
		switch ev.Op {
		case LogAlloc:
			if _, ok := fresh.table.Get(ev.Fec); ok {
				return nil, 0, fmt.Errorf("%w: log entry %d: fec already bound", ErrInvalidParam, i)
			}
			if !fresh.pool.AllocateSpecific(ev.Label) {
				return nil, 0, fmt.Errorf("%w: log entry %d: label unavailable", ErrInvalidParam, i)
			}
			fresh.table.SetBinding(ev.Fec, ev.Label)
		case LogFree:
			b, ok := fresh.table.Get(ev.Fec)
			if !ok || b.Label != ev.Label {
				return nil, 0, fmt.Errorf("%w: log entry %d: fec not bound to label", ErrInvalidParam, i)
			}
			fresh.table.RemoveBinding(ev.Fec)
			fresh.pool.Free(ev.Label, ev.Time)
			fresh.lastFree[ev.Fec] = freeRec{label: ev.Label, at: ev.Time}
		}
	}
	return fresh, maxLog, nil
}

// Log returns a copy of the journal: the restored prefix plus every ALLOC
// and FREE the manager has accepted since.
func (m *Manager) Log() []LogEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]LogEvent(nil), m.log...)
}

// Bindings returns a snapshot of the current fec-to-label mapping.
func (m *Manager) Bindings() map[string]int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.table.Bindings()
}
