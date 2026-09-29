// Package kvstate provides a concurrency-safe key value table with wall-clock expiration.
package kvstate

import (
	"sync"
	"time"
)

type entry struct {
	value        any
	lastActivity time.Time
	ttl          time.Duration
}

// Table stores values whose expiration is decided by an injected wall clock.
type Table struct {
	mu      sync.RWMutex
	now     func() time.Time
	entries map[string]entry
}

// NewTable creates a table. If now is nil, the system wall clock is used.
func NewTable(now func() time.Time) *Table {
	if now == nil {
		now = time.Now
	}
	return &Table{now: now, entries: make(map[string]entry)}
}

// Put stores value for key. Last activity is the greater of the previous activity and eventTime.
func (t *Table) Put(key string, value any, eventTime time.Time, ttl time.Duration) error {
	if key == "" {
		return ErrEmptyKey
	}
	if ttl <= 0 {
		return ErrInvalidTTL
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	current, exists := t.entries[key]
	if !exists || eventTime.After(current.lastActivity) {
		current.lastActivity = eventTime
	}
	current.value = value
	current.ttl = ttl
	t.entries[key] = current
	return nil
}

// Get returns a non-expired value for key and lazily removes expired entries.
func (t *Table) Get(key string) (any, bool) {
	if key == "" {
		return nil, false
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	current, exists := t.entries[key]
	if !exists {
		return nil, false
	}

	now := t.now()
	if isExpired(now, current.lastActivity, current.ttl) {
		delete(t.entries, key)
		return nil, false
	}
	return current.value, true
}

// PurgeExpired removes every entry that is expired at one observed wall-clock time.
func (t *Table) PurgeExpired() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	removed := 0
	for key, current := range t.entries {
		if isExpired(now, current.lastActivity, current.ttl) {
			delete(t.entries, key)
			removed++
		}
	}
	return removed
}

// Len returns the number of entries currently retained, including not-yet-checked expired entries.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return len(t.entries)
}

// Check verifies the table's internal validity without modifying state.
func (t *Table) Check() error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	for key, current := range t.entries {
		if key == "" {
			return ErrEmptyKey
		}
		if current.ttl <= 0 {
			return ErrInvalidTTL
		}
	}
	return nil
}

func isExpired(now, lastActivity time.Time, ttl time.Duration) bool {
	return now.Sub(lastActivity) >= ttl
}
