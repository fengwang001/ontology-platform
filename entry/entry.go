// Package entry implements the state machine of a single cache entry
// and its binding to a data version. An Entry is not safe for
// concurrent use on its own; callers (replica) must serialize access.
package entry

import (
	"time"

	"ontology/version"
)

// State is the lifecycle state of a cache entry.
type State int

const (
	// Hole means no data and no fetch has happened yet (zero value).
	Hole State = iota
	// Valid means the entry holds usable data (possibly a negative
	// cache marker) with a version and an expiry.
	Valid
	// Stale means the entry was invalidated or expired and must be
	// refetched before it can serve reads.
	Stale
	// Fetching means a backend fetch for this entry is in flight.
	Fetching
)

// String renders the state for logs and tests.
func (s State) String() string {
	switch s {
	case Hole:
		return "hole"
	case Valid:
		return "valid"
	case Stale:
		return "stale"
	case Fetching:
		return "fetching"
	}
	return "unknown"
}

// FetchResult is what a backend load returns for one key.
type FetchResult struct {
	Value   any
	Version version.Version
	Found   bool // false means the backend authoritatively says "absent"
}

// Entry is the state of one cached key.
type Entry struct {
	state     State
	ver       version.Version // held data version, or newest known invalidation
	value     any
	negative  bool // Valid entry caching the fact "key does not exist"
	expiresAt time.Time

	hits          uint64
	misses        uint64
	fetches       uint64
	invalidations uint64
}

// New returns a zero (Hole) entry.
func New() *Entry { return &Entry{} }

// State returns the current state.
func (e *Entry) State() State { return e.state }

// Version returns the held data version, or the newest known
// invalidation version when the entry holds no valid data.
func (e *Entry) Version() version.Version { return e.ver }

// Value returns the cached value; valid only in state Valid.
func (e *Entry) Value() any { return e.value }

// Negative reports whether the entry caches "key does not exist".
func (e *Entry) Negative() bool { return e.state == Valid && e.negative }

// ExpiresAt returns the expiry instant; valid only in state Valid.
func (e *Entry) ExpiresAt() time.Time { return e.expiresAt }

// Remaining returns the time until expiry, or 0 when not Valid.
func (e *Entry) Remaining(now time.Time) time.Duration {
	if e.state != Valid || !now.Before(e.expiresAt) {
		return 0
	}
	return e.expiresAt.Sub(now)
}

// ExpireIfDue applies lazy expiry with a left-closed, right-open
// liveness interval: now == expiresAt already means expired.
// It reports whether the entry transitioned Valid -> Stale.
func (e *Entry) ExpireIfDue(now time.Time) bool {
	if e.state == Valid && !now.Before(e.expiresAt) {
		e.state = Stale
		e.value = nil
		e.negative = false
		return true
	}
	return false
}

// ApplyInvalidate folds an invalidation notification into the entry.
// Notifications older than the known version are dropped untouched.
// It reports whether the notification actually changed state, which
// is exactly when the invalidation counter advances (idempotent
// under duplicate delivery).
func (e *Entry) ApplyInvalidate(nv version.Version) bool {
	if nv.Before(e.ver) {
		return false
	}
	switch e.state {
	case Valid:
		e.state = Stale
		e.value = nil
		e.negative = false
		e.ver = nv
		e.invalidations++
		return true
	case Hole, Stale, Fetching:
		if nv.After(e.ver) {
			e.ver = nv
			e.invalidations++
			return true
		}
	}
	return false
}

// BeginFetch marks the entry Fetching unless it is Valid.
func (e *Entry) BeginFetch() {
	if e.state != Valid {
		e.state = Fetching
	}
}

// CompleteFetch tries to store a fetch result. The result is
// discarded as stale when its version is older than the version
// known to the entry (e.g. an invalidation arrived mid-flight).
// It reports whether the result was stored.
func (e *Entry) CompleteFetch(fr FetchResult, now time.Time, ttl time.Duration) bool {
	if fr.Version.Before(e.ver) {
		if e.state != Valid {
			e.state = Stale
		}
		return false
	}
	e.state = Valid
	e.ver = fr.Version
	e.value = fr.Value
	e.negative = !fr.Found
	e.expiresAt = now.Add(ttl)
	return true
}

// FailFetch returns the entry to a refetchable state after a failed
// fetch. Nothing is cached; a Valid entry is left untouched.
func (e *Entry) FailFetch() {
	if e.state != Fetching {
		return
	}
	if e.ver.IsZero() {
		e.state = Hole
	} else {
		e.state = Stale
	}
}

// NoteHit records a cache hit.
func (e *Entry) NoteHit() { e.hits++ }

// NoteMiss records a cache miss.
func (e *Entry) NoteMiss() { e.misses++ }

// NoteFetch records one backend fetch attempt.
func (e *Entry) NoteFetch() { e.fetches++ }

// Counters returns the per-key hit/miss/fetch/invalidation counters.
func (e *Entry) Counters() (hits, misses, fetches, invalidations uint64) {
	return e.hits, e.misses, e.fetches, e.invalidations
}
