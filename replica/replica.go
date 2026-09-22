// Package replica is a cache replica: it serves reads, coordinates
// backend fetches through a singleflight group, applies invalidation
// notifications from a bus, and enforces TTL-based lazy expiry.
// All time comes from an injected clock.
package replica

import (
	"context"
	"errors"
	"sync"
	"time"

	"ontology/bus"
	"ontology/entry"
	"ontology/single"
)

// ErrEntriesFull is returned when a new entry cannot be created
// because the replica is at its entry limit and no expired entry
// can be evicted. The rejected operation changes no state.
var ErrEntriesFull = errors.New("replica: entry limit reached")

// Loader fetches one key from the backend.
type Loader func(ctx context.Context, key string) (entry.FetchResult, error)

// Clock supplies the current time; all TTL logic uses it.
type Clock func() time.Time

// Config configures a Replica.
type Config struct {
	Loader      Loader        // required
	Clock       Clock         // required, injected time source
	TTL         time.Duration // entry lifetime, must be > 0
	MaxEntries  int           // <= 0 means unbounded
	MaxInflight int           // singleflight limit, <= 0 means unbounded
}

// Replica is one cache replica. It is safe for concurrent use.
type Replica struct {
	mu         sync.Mutex
	loader     Loader
	clock      Clock
	ttl        time.Duration
	maxEntries int
	entries    map[string]*entry.Entry
	group      *single.Group

	// checked counts how many entries ApplyNotification inspected,
	// summed over all notifications. It is exactly 1 per notification,
	// proving invalidation application is O(1) in the cache size.
	checked uint64
}

// New creates a Replica. It panics on an incomplete config.
func New(cfg Config) *Replica {
	if cfg.Loader == nil || cfg.Clock == nil || cfg.TTL <= 0 {
		panic("replica: Loader, Clock and positive TTL are required")
	}
	return &Replica{
		loader:     cfg.Loader,
		clock:      cfg.Clock,
		ttl:        cfg.TTL,
		maxEntries: cfg.MaxEntries,
		entries:    make(map[string]*entry.Entry),
		group:      single.New(cfg.MaxInflight),
	}
}

// Subscribe wires this replica to a notification bus.
func (r *Replica) Subscribe(b *bus.Bus) {
	b.Subscribe(func(n bus.Notification) { _ = r.ApplyNotification(n) })
}

// Read returns the current value for key, fetching from the backend
// on a miss. Concurrent misses for the same key share one fetch.
func (r *Replica) Read(ctx context.Context, key string) (ReadResult, error) {
	r.mu.Lock()
	e := r.entries[key]
	if e != nil {
		e.ExpireIfDue(r.clock())
		if e.State() == entry.Valid {
			e.NoteHit()
			res := ReadResult{
				State:   Hit,
				Value:   e.Value(),
				Version: e.Version(),
				Found:   !e.Negative(),
			}
			if e.Negative() {
				res.State = Absent
			}
			r.mu.Unlock()
			return res, nil
		}
		e.NoteMiss()
	} else {
		var err error
		e, err = r.newEntryLocked(key)
		if err != nil {
			r.mu.Unlock()
			return ReadResult{}, err
		}
		e.NoteMiss()
	}
	r.mu.Unlock()

	fr, err := r.group.Do(ctx, key, func(ctx context.Context) (entry.FetchResult, error) {
		return r.fetch(ctx, key)
	})
	if err != nil && !errors.Is(err, errStaleFetch) {
		return ReadResult{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	e = r.entries[key]
	if e != nil && e.State() == entry.Valid {
		res := ReadResult{State: Hit, Value: e.Value(), Version: e.Version(), Found: !e.Negative()}
		if e.Negative() {
			res.State = Absent
		}
		return res, nil
	}
	// The fetch result was discarded as stale (a newer invalidation
	// arrived mid-flight). Report a miss; the next Read refetches.
	_ = fr
	res := ReadResult{State: Miss}
	if e != nil {
		res.Version = e.Version()
	}
	return res, nil
}

// fetch runs one backend fetch for key and folds the result into the
// entry. The loader runs without holding the replica lock, so a slow
// key never blocks other keys or notification delivery.
func (r *Replica) fetch(ctx context.Context, key string) (entry.FetchResult, error) {
	r.mu.Lock()
	e := r.entries[key]
	e.BeginFetch()
	e.NoteFetch()
	r.mu.Unlock()

	fr, err := r.loader(ctx, key)

	// Critical section: the staleness check and the store are atomic
	// with respect to ApplyNotification, because both run under r.mu.
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		e.FailFetch()
		return entry.FetchResult{}, err
	}
	if !e.CompleteFetch(fr, r.clock(), r.ttl) {
		return fr, errStaleFetch
	}
	return fr, nil
}

// errStaleFetch marks a fetch result discarded for being older than
// the version known to the entry. It is unexported on purpose: Read
// turns it into a plain miss.
var errStaleFetch = errors.New("replica: fetch result stale")

// newEntryLocked creates and registers an entry for key, evicting
// expired entries first. It fails with ErrEntriesFull, changing no
// state, when the replica is full of live entries.
func (r *Replica) newEntryLocked(key string) (*entry.Entry, error) {
	if r.maxEntries > 0 && len(r.entries) >= r.maxEntries {
		r.evictExpiredLocked(r.clock())
		if len(r.entries) >= r.maxEntries {
			return nil, ErrEntriesFull
		}
	}
	e := entry.New()
	r.entries[key] = e
	return e, nil
}

// evictExpiredLocked removes every entry whose TTL has already
// elapsed. Expired entries are semantically dead (any read would
// refetch them), so evicting them changes no observable behavior.
func (r *Replica) evictExpiredLocked(now time.Time) {
	for k, e := range r.entries {
		if e.State() == entry.Valid && !now.Before(e.ExpiresAt()) {
			delete(r.entries, k)
		}
	}
}

// FetchCalls returns how many times a fetch flight actually ran.
func (r *Replica) FetchCalls() uint64 { return r.group.Calls() }
