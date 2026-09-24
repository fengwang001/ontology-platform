// Package hh manages multiple replicas: online/down transitions, writes
// applied to online replicas and parked as hints for down ones, and in-order
// replay on recovery. It depends only on package hint.
package hh

import (
	"errors"
	"sync"

	"ontology/hint"
)

// Four distinct, decidable failure classes.
var (
	ErrBadConfig    = errors.New("hh: invalid configuration")
	ErrBadVersion   = errors.New("hh: version must be > 0")
	ErrEmptyKey     = errors.New("hh: key must not be empty")
	ErrHintOverflow = errors.New("hh: hint buffer overflow")
)

// Entry is one replica's current value/version for a key (zero value = none).
type Entry struct {
	Value string
	Ver   int64
}

type replica struct {
	up      bool
	entries map[string]Entry
	hbuf    *hint.Buffer
}

// Manager is the multi-replica hinted-handoff state; safe for concurrent use.
type Manager struct {
	mu       sync.Mutex
	replicas []*replica
}

// New creates n initially-online replicas, each with the given hint capacity.
func New(n, maxHints int) (*Manager, error) {
	if n < 1 || maxHints <= 0 {
		return nil, ErrBadConfig
	}
	rs := make([]*replica, n)
	for i := range rs {
		rs[i] = &replica{up: true, entries: map[string]Entry{}, hbuf: hint.New(maxHints)}
	}
	return &Manager{replicas: rs}, nil
}

// N reports the replica count.
func (m *Manager) N() int { return len(m.replicas) }

// Write strictly updates online replicas and appends an unconditional hint for
// each down replica. If any down buffer is full it fails atomically (before
// touching any replica or buffer) with ErrHintOverflow.
func (m *Manager) Write(key, value string, ver int64) error {
	if ver <= 0 {
		return ErrBadVersion
	}
	if key == "" {
		return ErrEmptyKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rp := range m.replicas { // pre-flight: full buffer aborts atomically
		if !rp.up && rp.hbuf.Full() {
			return ErrHintOverflow
		}
	}
	for _, rp := range m.replicas {
		if rp.up {
			if cur := rp.entries[key]; hint.Apply(ver, cur.Ver) {
				rp.entries[key] = Entry{Value: value, Ver: ver}
			}
		} else { // stale/tie versions are filtered strictly at replay time
			rp.hbuf.Append(hint.Entry{Key: key, Value: value, Ver: ver})
		}
	}
	return nil
}

// Down marks a replica down (repeated Down is a no-op).
func (m *Manager) Down(r int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r < 0 || r >= len(m.replicas) {
		return ErrBadConfig
	}
	m.replicas[r].up = false
	return nil
}

// Up marks a replica up, replays hints in append order under the strict >
// rule and returns (applied, skipped).
func (m *Manager) Up(r int) (applied, skipped int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r < 0 || r >= len(m.replicas) {
		return 0, 0, ErrBadConfig
	}
	rp := m.replicas[r]
	rp.up = true
	a, sk := rp.hbuf.Replay(
		func(k string) int64 { return rp.entries[k].Ver },
		func(e hint.Entry) { rp.entries[e.Key] = Entry{Value: e.Value, Ver: e.Ver} },
	)
	return a, sk, nil
}

// IsUp reports whether replica r is online (false for out-of-range indexes).
func (m *Manager) IsUp(r int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return r >= 0 && r < len(m.replicas) && m.replicas[r].up
}

// Get returns a snapshot of every replica's entry for key.
func (m *Manager) Get(key string) []Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Entry, len(m.replicas))
	for i, rp := range m.replicas {
		out[i] = rp.entries[key]
	}
	return out
}

// Hints returns a copy of replica r's buffered hints in append order. The
// package-hint dedup-scan counter is intentionally not exposed.
func (m *Manager) Hints(r int) []hint.Entry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r < 0 || r >= len(m.replicas) {
		return nil
	}
	return m.replicas[r].hbuf.Snapshot()
}
