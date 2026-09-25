// Package wcount keeps a per-key high watermark and cumulative accept
// count, one global cumulative drop count, and applies events in batches
// that either fully succeed or fully fail. It depends only on package slack.
package wcount

import (
	"errors"
	"sync"

	"ontology/slack"
)

// Sentinel errors. The public api package aliases these so callers can use
// errors.Is; the three failure modes are mutually distinct.
var (
	// ErrEmptyKey: an event carried the empty key.
	ErrEmptyKey = errors.New("wcount: event key must not be empty")
	// ErrTooManyKeys: the batch would raise the distinct-key count past maxKeys.
	ErrTooManyKeys = errors.New("wcount: distinct key count would exceed maxKeys")
	// ErrInvalidArgs: K negative or maxKeys non-positive at construction.
	ErrInvalidArgs = errors.New("wcount: require K >= 0 and maxKeys > 0")
)

// Event is one upstream change event with its source sequence number.
type Event struct {
	Key string
	Seq int64
}

// Counter is the in-process, concurrency-safe counter.
type Counter struct {
	mu sync.Mutex

	k       int64
	maxKeys int

	high     map[string]slack.State
	accepted map[string]int64
	dropped  int64

	// probe records how many keys were examined to locate the high
	// watermark of the most recently processed event. A map lookup examines
	// exactly one entry, so it never grows with the number of keys. It is
	// deliberately unexported: only in-package tests may read it; no
	// exported function or method exposes its value.
	probe int
}

// New validates parameters and returns an empty counter.
func New(k int64, maxKeys int) (*Counter, error) {
	if k < 0 || maxKeys <= 0 {
		return nil, ErrInvalidArgs
	}
	return &Counter{
		k:        k,
		maxKeys:  maxKeys,
		high:     make(map[string]slack.State),
		accepted: make(map[string]int64),
	}, nil
}

// Feed applies a whole batch or none of it. Validation runs first against a
// dry-run set of newly introduced keys; only if every event passes does any
// state change happen, so high watermarks and counters survive a rejected
// batch byte-for-byte and the counter stays usable afterwards.
func (c *Counter) Feed(evs []Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	introduced := make(map[string]struct{})
	for _, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
		if _, ok := c.high[e.Key]; !ok {
			introduced[e.Key] = struct{}{}
		}
	}
	if len(c.high)+len(introduced) > c.maxKeys {
		return ErrTooManyKeys
	}

	for _, e := range evs {
		// Locate this key's watermark: one map probe, independent of m.
		st := c.high[e.Key]
		c.probe = 1
		switch slack.Decide(&st, e.Seq, c.k) {
		case slack.Drop:
			c.dropped++ // drops never touch high (Decide left st unchanged)
		default:
			c.accepted[e.Key]++
		}
		c.high[e.Key] = st
	}
	return nil
}

// Accepted returns the cumulative accepted-event count for key.
func (c *Counter) Accepted(key string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accepted[key]
}

// Dropped returns the cumulative out-of-window drop count across all keys.
func (c *Counter) Dropped() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropped
}

// High reports the key's high watermark and whether the key is known.
func (c *Counter) High(key string) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.high[key]
	return st.High, ok
}
