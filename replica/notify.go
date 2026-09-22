package replica

import (
	"time"

	"ontology/bus"
	"ontology/entry"
	"ontology/version"
)

// ReadState classifies the outcome of a Read.
type ReadState int

const (
	// Miss: no usable data (never fetched, stale, or fetch discarded).
	Miss ReadState = iota
	// Hit: live cached data.
	Hit
	// Absent: the backend authoritatively said the key does not
	// exist, and that fact is cached (negative cache).
	Absent
)

// String renders the read state for logs and tests.
func (s ReadState) String() string {
	switch s {
	case Miss:
		return "miss"
	case Hit:
		return "hit"
	case Absent:
		return "absent"
	}
	return "unknown"
}

// ReadResult is the outcome of one Read call.
type ReadResult struct {
	State   ReadState
	Value   any             // valid only when State == Hit
	Version version.Version // data version, or newest known version on Miss
	Found   bool            // false when the backend says "absent"
}

// KeyInfo is a read-only snapshot of one key, as returned by Inspect.
// The zero value (State Hole) describes an unknown key.
type KeyInfo struct {
	State         entry.State
	Version       version.Version
	Remaining     time.Duration
	Hits          uint64
	Misses        uint64
	Fetches       uint64
	Invalidations uint64
}

// ApplyNotification folds one invalidation notification into the
// replica. It touches exactly the affected key (checked grows by
// exactly one per call) and never scans the entry map.
func (r *Replica) ApplyNotification(n bus.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checked++
	e := r.entries[n.Key]
	if e == nil {
		var err error
		e, err = r.newEntryLocked(n.Key)
		if err != nil {
			return err
		}
	}
	e.ExpireIfDue(r.clock())
	e.ApplyInvalidate(n.Version)
	return nil
}

// Inspect returns a snapshot of key's state, version, remaining TTL,
// and counters. It never mutates anything beyond lazy expiry, so two
// Inspect calls at the same instant return identical results. An
// unknown key yields the zero KeyInfo (State Hole).
func (r *Replica) Inspect(key string) KeyInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[key]
	if e == nil {
		return KeyInfo{State: entry.Hole}
	}
	now := r.clock()
	e.ExpireIfDue(now)
	hits, misses, fetches, invalidations := e.Counters()
	return KeyInfo{
		State:         e.State(),
		Version:       e.Version(),
		Remaining:     e.Remaining(now),
		Hits:          hits,
		Misses:        misses,
		Fetches:       fetches,
		Invalidations: invalidations,
	}
}

// Len returns how many entries the replica currently holds.
func (r *Replica) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// Checked returns how many entry probes notification application has
// performed in total. It grows by exactly one per notification,
// independent of how many entries the replica holds.
func (r *Replica) Checked() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.checked
}
