// Package api is the public entry point for out-of-order event counting
// with a per-key K-slack tolerance window. All state is in-process memory;
// it depends only on package wcount (which depends on package slack).
package api

import (
	"errors"
	"fmt"

	"ontology/wcount"
)

// Event is one upstream change event. Event is an alias of wcount.Event so
// callers build slices with the exact type Feed accepts.
type Event = wcount.Event

// The three distinct, decidable failure modes.
var (
	// ErrEmptyKey: an event carried the empty key.
	ErrEmptyKey = wcount.ErrEmptyKey
	// ErrTooManyKeys: a batch would exceed the distinct-key limit.
	ErrTooManyKeys = wcount.ErrTooManyKeys
	// ErrInvalidArgs: K < 0 or maxKeys <= 0 at construction.
	ErrInvalidArgs = wcount.ErrInvalidArgs
)

// W is the concurrency-safe counter handle.
type W struct {
	c *wcount.Counter
}

// New creates a counter with tolerance window K and a cap of maxKeys
// distinct keys. It returns ErrInvalidArgs for K < 0 or maxKeys <= 0.
func New(K int64, maxKeys int) (*W, error) {
	c, err := wcount.New(K, maxKeys)
	if err != nil {
		return nil, err
	}
	return &W{c: c}, nil
}

// Feed applies all events or none of them.
func (w *W) Feed(evs []Event) error { return w.c.Feed(evs) }

// Accepted returns the cumulative accepted count for key (0 if unknown).
func (w *W) Accepted(key string) int64 { return w.c.Accepted(key) }

// Dropped returns the cumulative out-of-window drop count over all keys.
func (w *W) Dropped() int64 { return w.c.Dropped() }

// High reports key's high watermark and whether the key is known.
func (w *W) High(key string) (int64, bool) { return w.c.High(key) }

// naiveStep is the reference "recompute one event at a time" semantics,
// kept independent from slack/wcount so the check is not circular.
func naiveStep(high map[string]int64, seen map[string]bool, K int64, e Event) (accept bool) {
	h := high[e.Key]
	switch {
	case !seen[e.Key]:
		high[e.Key], seen[e.Key] = e.Seq, true
		return true
	case e.Seq > h:
		high[e.Key] = e.Seq
		return true
	default:
		return h-e.Seq <= K // closed window: seq == high-K accepted
	}
}

// SelfCheck runs a built-in event set and verifies all four invariants:
// naive equivalence, boundary/non-retreat consistency, monotonic counters,
// and all-or-nothing failure. It works on internal fresh counters and never
// mutates the receiver. A nil result means every check passed.
func (w *W) SelfCheck() error {
	const K int64 = 3
	c, err := New(K, 16)
	if err != nil {
		return err
	}
	seqs := []int64{10, 8, 12, 5, 8, 11, 9, 7}
	nh, ns := map[string]int64{}, map[string]bool{}
	var nAcc, nDrop, prevA, prevD int64
	for _, seq := range seqs {
		e := Event{Key: "a", Seq: seq}
		if err := c.Feed([]Event{e}); err != nil {
			return fmt.Errorf("selfcheck: feed failed: %w", err)
		}
		if naiveStep(nh, ns, K, e) {
			nAcc++
		} else {
			nDrop++
		}
		if c.Accepted("a") != nAcc || c.Dropped() != nDrop { // invariant 1
			return errors.New("selfcheck: invariant 1 (naive equivalence) violated")
		}
		if h, _ := c.High("a"); h != nh["a"] { // invariant 2: high matches
			return errors.New("selfcheck: invariant 2 (high watermark) violated")
		}
		if c.Accepted("a") < prevA || c.Dropped() < prevD { // invariant 3
			return errors.New("selfcheck: invariant 3 (monotonic counters) violated")
		}
		prevA, prevD = c.Accepted("a"), c.Dropped()
	}
	if c.Accepted("a") != 5 || c.Dropped() != 3 {
		return errors.New("selfcheck: canonical totals wrong")
	}

	// Boundary micro-sequence: high-K accepted, high-K-1 dropped, high fixed.
	b, _ := New(K, 4)
	if err := b.Feed([]Event{{Key: "a", Seq: 10}}); err != nil {
		return err
	}
	if err := b.Feed([]Event{{Key: "a", Seq: 7}}); err != nil || b.Accepted("a") != 2 {
		return errors.New("selfcheck: seq == high-K must be accepted (left closed)")
	}
	if h, _ := b.High("a"); h != 10 {
		return errors.New("selfcheck: in-window event must not retreat high")
	}
	if err := b.Feed([]Event{{Key: "a", Seq: 6}}); err != nil || b.Dropped() != 1 {
		return errors.New("selfcheck: seq == high-K-1 must be dropped")
	}
	if h, _ := b.High("a"); h != 10 {
		return errors.New("selfcheck: dropped event must not change high")
	}

	// Invariant 4: rejected batches leave no trace, instance stays usable.
	snapA := c.Accepted("a")
	err = c.Feed([]Event{{Key: "a", Seq: 99}, {Key: "", Seq: 1}})
	if !errors.Is(err, ErrEmptyKey) || c.Accepted("a") != snapA {
		return errors.New("selfcheck: empty-key batch not atomic")
	}
	if h, ok := c.High("a"); !ok || h != 12 {
		return errors.New("selfcheck: empty-key batch moved high")
	}
	if err := c.Feed([]Event{{Key: "a", Seq: 13}}); err != nil || c.Accepted("a") != snapA+1 {
		return errors.New("selfcheck: counter unusable after rejected batch")
	}
	small, _ := New(K, 1)
	if err := small.Feed([]Event{{Key: "x", Seq: 1}}); err != nil {
		return err
	}
	if err := small.Feed([]Event{{Key: "y", Seq: 1}}); !errors.Is(err, ErrTooManyKeys) {
		return errors.New("selfcheck: maxKeys overflow not rejected")
	}
	if _, ok := small.High("y"); ok || small.Dropped() != 0 {
		return errors.New("selfcheck: overflow batch left a trace")
	}
	return nil
}
