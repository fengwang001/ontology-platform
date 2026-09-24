// Package mvcc holds the global version clock, the active snapshot set and
// the per-key copy-on-write version chains. A single RWMutex serializes
// writers; readers take the read lock, so Read never blocks Write-free
// readers and old snapshots always see their own slice of history.
package mvcc

import (
	"sort"
	"sync"

	"ontology/chain"
)

// Sentinel errors are distinct values decidable via errors.Is.
var (
	ErrEmptyKey         = errMVCC("mvcc: key must not be empty")
	ErrSnapshotRange    = errMVCC("mvcc: snapshot is negative or greater than t")
	ErrSnapshotInactive = errMVCC("mvcc: snapshot is not in the active set")
)

type errMVCC string

func (e errMVCC) Error() string { return string(e) }

// MVCC is the process-local multi-version store.
type MVCC struct {
	mu    sync.RWMutex
	t     int64
	keys  map[string]*chain.Chain
	alive map[int64]struct{}
}

// New returns an empty store with the global clock at zero.
func New() *MVCC {
	return &MVCC{keys: map[string]*chain.Chain{}, alive: map[int64]struct{}{}}
}

// Write validates first (state untouched on rejection), then advances t,
// prepends a fresh (value, t) node to key's chain and returns t.
func (m *MVCC) Write(key, value string) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.t++
	c := m.keys[key]
	if c == nil {
		c = chain.New()
		m.keys[key] = c
	}
	c.Prepend(value, m.t)
	return m.t, nil
}

// Snapshot returns the current t and adds it to the active snapshot set.
func (m *MVCC) Snapshot() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alive[m.t] = struct{}{}
	return m.t
}

// Read returns the greatest version of key with ver <= s. A missing key
// (or no visible version) yields found=false. Only range is checked: s
// must satisfy 0 <= s <= t; it need not itself be an active snapshot.
func (m *MVCC) Read(key string, s int64) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s < 0 || s > m.t {
		return "", false, ErrSnapshotRange
	}
	if c := m.keys[key]; c != nil {
		v, found := c.Visible(s)
		return v, found, nil
	}
	return "", false, nil
}

// Release removes s from the active set; an unknown or already released
// snapshot fails with ErrSnapshotInactive and changes nothing.
func (m *MVCC) Release(s int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.alive[s]; !ok {
		return ErrSnapshotInactive
	}
	delete(m.alive, s)
	return nil
}

// Collect reclaims, on every chain, non-head versions whose [v, v')
// interval contains no active snapshot, and returns the removed count.
func (m *MVCC) Collect() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	active := make([]int64, 0, len(m.alive))
	for s := range m.alive {
		active = append(active, s)
	}
	sort.Slice(active, func(i, j int) bool { return active[i] < active[j] })
	n := 0
	for _, c := range m.keys {
		n += c.Collect(active)
	}
	return n
}

// Active reports whether s is currently registered.
func (m *MVCC) Active(s int64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.alive[s]
	return ok
}

// T returns the current global version.
func (m *MVCC) T() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.t
}
