// Package wdist keeps only each key's latest timestamp and the exact
// window distinct count. Expired whole buckets drop arithmetically
// (one boundary comparison per trigger); stranded edge-bucket keys
// leave via the ordered cross queue. Depends only on package win.
package wdist

import (
	"errors"

	"ontology/win"
)

// Event is a change arriving with its event time.
type Event struct {
	Key int
	TS  int64
}

// Sentinel errors; api exposes the same four categories to callers.
var (
	// ErrInvalidEvent: negative Key or negative TS.
	ErrInvalidEvent = errors.New("wdist: key and timestamp must be non-negative")
	// ErrTSRollback: a TS smaller than an already accepted one.
	ErrTSRollback = errors.New("wdist: timestamp regression")
)

type qent struct {
	key int
	ts  int64
}

// Counter holds all state in process memory; zero value is not usable.
type Counter struct {
	W, b int64

	last map[int]int64          // latest TS per key
	bk   map[int64]map[int]bool // bucket idx -> keys currently in it
	q    []qent                 // append-ordered (TS non-decreasing) history
	head int64                  // oldest bucket index that may hold keys
	t    int64                  // latest accepted TS
	fed  bool
	live int

	// checked: number of buckets whose boundary was COMPARED with the
	// cutoff during the latest expiry trigger. Unexported by contract;
	// only same-package tests may read it.
	checked int64
}

// New creates a counter. W and b are assumed already validated by api.
func New(W, b int64) *Counter {
	return &Counter{W: W, b: b, last: map[int]int64{}, bk: map[int64]map[int]bool{}}
}

// Check validates a whole batch against current state without mutating
// anything: either the batch is acceptable as-is or nothing applies.
func (c *Counter) Check(evs []Event) error {
	t := c.t
	for _, e := range evs {
		if e.Key < 0 || e.TS < 0 {
			return ErrInvalidEvent
		}
		if c.fed && e.TS < t {
			return ErrTSRollback
		}
		t = e.TS
	}
	return nil
}

// Apply records an already validated batch, then expires once at the
// batch's final T. last[key] only ever moves to an equal/larger TS.
func (c *Counter) Apply(evs []Event) {
	for _, e := range evs {
		if !c.fed || e.TS > c.t {
			c.t = e.TS
		}
		c.fed = true
		if old, ok := c.last[e.Key]; ok {
			c.removeFromBucket(old, e.Key)
		} else {
			c.live++ // first appearance, or reappearance after expiry
		}
		idx := win.Bucket(e.TS, c.b)
		s := c.bk[idx]
		if s == nil {
			s = map[int]bool{}
			c.bk[idx] = s
		}
		s[e.Key] = true
		c.last[e.Key] = e.TS
		c.q = append(c.q, qent{e.Key, e.TS})
	}
	c.expire(c.t)
}

// T returns the latest accepted timestamp.
func (c *Counter) T() int64 { return c.t }

// Distinct returns keys with last[key] in (T-W, T].
func (c *Counter) Distinct() int { return c.live }

// removeFromBucket drops key from the bucket holding its old last TS.
func (c *Counter) removeFromBucket(oldTS int64, key int) {
	idx := win.Bucket(oldTS, c.b)
	if s := c.bk[idx]; s != nil {
		delete(s, key)
		if len(s) == 0 {
			delete(c.bk, idx)
		}
	}
}

// expire drops everything at or left of cutoff = T-W: whole expired
// buckets via one arithmetic boundary comparison, then exact per-key
// entries from the surviving edge bucket via the ordered queue.
func (c *Counter) expire(T int64) {
	c.checked = 0
	cutoff := T - c.W
	if len(c.bk) > 0 {
		// The single boundary comparison: does even the oldest bucket
		// survive? Arithmetic then names the whole droppable prefix.
		c.checked = 1
		if win.BucketEnd(c.head, c.b) <= cutoff {
			max := win.MaxExpiredBucket(T, c.W, c.b)
			for idx := c.head; idx <= max; idx++ {
				for k := range c.bk[idx] {
					delete(c.last, k)
					c.live--
				}
				delete(c.bk, idx)
			}
			c.head = max + 1
		}
	}
	// Exact semantics inside the edge bucket: pop the ordered head.
	for len(c.q) > 0 {
		h := c.q[0]
		if h.ts > cutoff {
			break // queue is TS-ordered; every later entry is newer
		}
		c.q = c.q[1:]
		if cur, ok := c.last[h.key]; ok && cur == h.ts { // stale: superseded move or bucket-pass delete
			delete(c.last, h.key)
			c.removeFromBucket(h.ts, h.key)
			c.live--
		}
	}
}
