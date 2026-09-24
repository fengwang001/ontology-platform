// Package hh manages hinted handoff across n in-memory replicas.
//
// One mutex guards all state; Write validates and runs a full preflight (every
// down replica must have room) before mutating anything, so a rejected write
// leaves no trace on online or down replicas alike.
package hh

import (
	"errors"
	"sync"

	"ontology/hint"
)

// The four error categories are mutually distinct, decision-ready sentinels.
var (
	ErrConfig       = errors.New("hh: invalid configuration or replica index")
	ErrVersion      = errors.New("hh: ver must be > 0")
	ErrKey          = errors.New("hh: key must not be empty")
	ErrHintOverflow = errors.New("hh: hint buffer overflow")
)

// Entry is one replica's current value for a key; Set=false means never set,
// distinguishing "absent" from a zero value.
type Entry struct {
	Value string
	Ver   int64
	Set   bool
}

type replica struct {
	up   bool
	data map[string]Entry
	buf  *hint.Buffer
}

// Cluster is a set of replicas with per-replica hint buffers.
type Cluster struct {
	mu   sync.Mutex
	reps []*replica
}

// New creates n initially-online replicas whose down buffers hold maxHints.
func New(n, maxHints int) (*Cluster, error) {
	if n < 1 || maxHints <= 0 {
		return nil, ErrConfig
	}
	c := &Cluster{reps: make([]*replica, n)}
	for i := range c.reps {
		c.reps[i] = &replica{up: true, data: make(map[string]Entry), buf: hint.NewBuffer(maxHints)}
	}
	return c, nil
}

// Write applies a strict newer-than update to online replicas and appends a
// hint to each down replica. It is all-or-nothing: a full buffer rejects
// before any state changes.
func (c *Cluster) Write(key, value string, ver int64) error {
	if key == "" {
		return ErrKey
	}
	if ver <= 0 {
		return ErrVersion
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rp := range c.reps { // preflight before mutating anything
		if !rp.up && rp.buf.WouldOverflow() {
			return ErrHintOverflow
		}
	}
	h := hint.Entry{Key: key, Value: value, Ver: ver}
	for _, rp := range c.reps {
		if rp.up {
			if cur := rp.data[key]; hint.ShouldApply(ver, cur.Ver) {
				rp.data[key] = Entry{Value: value, Ver: ver, Set: true}
			}
			continue
		}
		if err := rp.buf.Append(h); err != nil { // unreachable after preflight
			return ErrHintOverflow
		}
	}
	return nil
}

// Down marks a replica offline; later writes become hints for it.
func (c *Cluster) Down(r int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r < 0 || r >= len(c.reps) {
		return ErrConfig
	}
	c.reps[r].up = false
	return nil
}

// Up brings r online and replays its hints in append order, applying only
// strictly newer entries (stale/tied skipped), then clears the buffer.
func (c *Cluster) Up(r int) (applied, skipped int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r < 0 || r >= len(c.reps) {
		return 0, 0, ErrConfig
	}
	rp := c.reps[r]
	rp.up = true
	applied, skipped = rp.buf.Replay(func(h hint.Entry) bool {
		if !hint.ShouldApply(h.Ver, rp.data[h.Key].Ver) {
			return false
		}
		rp.data[h.Key] = Entry{Value: h.Value, Ver: h.Ver, Set: true}
		return true
	})
	return applied, skipped, nil
}

// Snapshot returns a per-replica copy of the current entry for key.
func (c *Cluster) Snapshot(key string) []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Entry, len(c.reps))
	for i, rp := range c.reps {
		out[i] = rp.data[key]
	}
	return out
}

// Hints returns a copy of replica r's buffered hints in append order.
func (c *Cluster) Hints(r int) []hint.Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]hint.Entry(nil), c.reps[r].buf.Snapshot()...)
}
