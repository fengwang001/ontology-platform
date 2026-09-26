// Package mgr keeps named leases in memory and locates expired ones with a
// min-heap ordered by expiry. It depends only on package lease.
package mgr

import (
	"container/heap"
	"errors"
	"sort"
	"sync"

	"ontology/lease"
)

// Distinguishable sentinel errors, all different from lease.ErrStaleToken
// and lease.ErrExpired.
var (
	// ErrEmptyName is returned when name is the empty string.
	ErrEmptyName = errors.New("mgr: lease name is empty")
	// ErrNotAcquired is returned by Renew for a name that was never
	// Acquired (Expired on such a name is defined as true, not an error).
	ErrNotAcquired = errors.New("mgr: lease was never acquired")
)

// heapEntry is one heap position. Renew/Grant push a fresh entry instead
// of mutating the old one; stale entries are dropped when they reach the
// top, identified by (token, expiry) no longer matching the live lease.
type heapEntry struct {
	name   string
	token  int
	expiry int
}

type expiryHeap []heapEntry

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].expiry < h[j].expiry }
func (h expiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *expiryHeap) Push(x any) { *h = append(*h, x.(heapEntry)) }
func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	*h = old[:n-1]
	return e
}

// Manager is the in-memory lease manager. The zero value is not usable;
// use New.
type Manager struct {
	ttl    int
	mu     sync.RWMutex
	leases map[string]*lease.Lease
	order  expiryHeap

	// scanned is the number of heap entries inspected during the most
	// recent ExpiredAll call. It is unexported on purpose: callers prove
	// heap use through behaviour, never by reading this number.
	scanned int
}

// New creates a manager with the fixed time-to-live ttl.
func New(ttl int) *Manager {
	m := &Manager{ttl: ttl, leases: map[string]*lease.Lease{}}
	heap.Init(&m.order)
	return m
}

// Acquire grants name to owner at now: token strictly increases and expiry
// becomes now+TTL. It returns the new fencing token. An empty name is the
// only refusal.
func (m *Manager) Acquire(name, owner string, now int) (int, error) {
	if name == "" {
		return 0, ErrEmptyName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[name]
	if !ok {
		l = lease.New(m.ttl)
		m.leases[name] = l
	}
	token := l.Grant(owner, now)
	heap.Push(&m.order, heapEntry{name: name, token: token, expiry: l.Expiry()})
	return token, nil
}

// Renew heartbeats name at now using token. It fails with ErrEmptyName,
// ErrNotAcquired, lease.ErrStaleToken or lease.ErrExpired. All validation
// happens before any map/heap write, so a refusal leaves no trace.
func (m *Manager) Renew(name string, token, now int) error {
	if name == "" {
		return ErrEmptyName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.leases[name]
	if !ok {
		return ErrNotAcquired
	}
	if err := l.Renew(token, now); err != nil {
		return err
	}
	heap.Push(&m.order, heapEntry{name: name, token: token, expiry: l.Expiry()})
	return nil
}

// Expired reports now >= expiry for name. A name never Acquired is expired.
// Safe for concurrent use.
func (m *Manager) Expired(name string, now int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.leases[name]
	if !ok {
		return true
	}
	return l.Expired(now)
}

// ExpiredAll returns the names of all leases expired at now, sorted. Only
// entries up to the first heap top past now are touched, so the work is
// proportional to the expired count, not to the number of leases.
func (m *Manager) ExpiredAll(now int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scanned = 0
	var live []heapEntry // valid expired entries, pushed back afterwards
	var names []string
	for m.order.Len() > 0 {
		top := m.order[0]
		if top.expiry > now {
			break
		}
		heap.Pop(&m.order)
		m.scanned++
		l, ok := m.leases[top.name]
		if !ok || l.Token() != top.token || l.Expiry() != top.expiry {
			continue // stale entry: superseded by a later Grant/Renew
		}
		names = append(names, top.name)
		live = append(live, top)
	}
	for i := range live {
		heap.Push(&m.order, live[i])
	}
	sort.Strings(names)
	return names
}
