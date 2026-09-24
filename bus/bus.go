// Package bus fans each published event out to N independent bounded
// subscriber queues. A full queue for one subscriber only tail-drops for
// that subscriber; it never blocks the publisher or affects others.
package bus

import (
	"errors"

	"ontology/fanout"
)

// ErrInvalidN is returned when the subscriber count is <= 0.
var ErrInvalidN = errors.New("bus: subscriber count must be >= 1")

// ErrInvalidCapacity is returned when the shared queue capacity is <= 0.
var ErrInvalidCapacity = errors.New("bus: queue capacity must be >= 1")

// ErrOutOfRange is returned when a subscriber index is not in [0, N).
var ErrOutOfRange = errors.New("bus: subscriber index out of range")

// Bus holds one independent fanout.Queue per subscriber.
type Bus struct {
	subs []*fanout.Queue
}

// New builds a bus with n subscribers, each with a capacity-c queue.
// It validates before constructing anything, so a rejection changes no
// state at all.
func New(n, c int) (*Bus, error) {
	if n <= 0 {
		return nil, ErrInvalidN
	}
	if c <= 0 {
		return nil, ErrInvalidCapacity
	}
	subs := make([]*fanout.Queue, n)
	for i := range subs {
		q, err := fanout.New(c)
		if err != nil { // already guarded; keep construction total
			return nil, err
		}
		subs[i] = q
	}
	return &Bus{subs: subs}, nil
}

// Publish broadcasts ev to every subscriber independently. Each
// subscriber's Enqueue is a separate call: a full queue tail-drops for
// that subscriber only, so the publisher never blocks and no other
// subscriber is starved.
func (b *Bus) Publish(ev int64) {
	for _, q := range b.subs {
		q.Enqueue(ev)
	}
}

// Consume pops subscriber si's head event; ok is false when empty.
func (b *Bus) Consume(si int) (int64, bool, error) {
	if si < 0 || si >= len(b.subs) {
		return 0, false, ErrOutOfRange
	}
	ev, ok := b.subs[si].Dequeue()
	return ev, ok, nil
}

// DropCount returns subscriber si's cumulative dropped events.
func (b *Bus) DropCount(si int) (int, error) {
	if si < 0 || si >= len(b.subs) {
		return 0, ErrOutOfRange
	}
	return b.subs[si].DropCount(), nil
}

// QueueLen returns subscriber si's current queue length.
func (b *Bus) QueueLen(si int) (int, error) {
	if si < 0 || si >= len(b.subs) {
		return 0, ErrOutOfRange
	}
	return b.subs[si].Len(), nil
}
