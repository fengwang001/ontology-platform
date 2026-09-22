// Package bus is an in-process invalidation-notification bus.
// Notifications are queued on Publish and delivered to subscribers
// on Flush. The bus provides hooks to inject out-of-order delivery,
// duplicate delivery, and loss, as real notification systems do.
package bus

import (
	"errors"
	"math/rand"
	"sync"

	"ontology/version"
)

// ErrQueueFull is returned by Publish when the pending queue is at
// its configured capacity. The rejected publish changes no state.
var ErrQueueFull = errors.New("bus: pending queue full")

// Notification is one invalidation notice for one key.
type Notification struct {
	Key     string
	Version version.Version
}

// Bus fans notifications out to subscribers. Replicas never talk to
// each other directly; they only subscribe to a bus.
type Bus struct {
	mu       sync.Mutex
	subs     []func(Notification)
	queue    []Notification
	maxQueue int

	published uint64
	delivered uint64
	dropped   uint64
}

// New creates a Bus whose pending queue holds at most maxQueue
// notifications. maxQueue <= 0 means unbounded.
func New(maxQueue int) *Bus { return &Bus{maxQueue: maxQueue} }

// Subscribe registers fn to receive delivered notifications.
func (b *Bus) Subscribe(fn func(Notification)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = append(b.subs, fn)
}

// Publish enqueues one notification for later delivery.
func (b *Bus) Publish(n Notification) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.maxQueue > 0 && len(b.queue) >= b.maxQueue {
		return ErrQueueFull
	}
	b.queue = append(b.queue, n)
	b.published++
	return nil
}

// Duplicate enqueues the same notification twice, simulating the
// duplicate delivery real buses exhibit.
func (b *Bus) Duplicate(n Notification) error {
	if err := b.Publish(n); err != nil {
		return err
	}
	return b.Publish(n)
}

// DropOldest discards the oldest pending notification, simulating
// message loss. It reports whether anything was dropped.
func (b *Bus) DropOldest() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.queue) == 0 {
		return false
	}
	b.queue = b.queue[1:]
	b.dropped++
	return true
}

// Pending returns the number of queued, undelivered notifications.
func (b *Bus) Pending() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queue)
}

// Flush delivers all pending notifications in FIFO order and
// returns how many were delivered.
func (b *Bus) Flush() int {
	subs, batch := b.take()
	for _, n := range batch {
		for _, fn := range subs {
			fn(n)
		}
	}
	return len(batch)
}

// FlushShuffled delivers all pending notifications in a random
// order, injecting out-of-order delivery.
func (b *Bus) FlushShuffled(r *rand.Rand) int {
	subs, batch := b.take()
	r.Shuffle(len(batch), func(i, j int) {
		batch[i], batch[j] = batch[j], batch[i]
	})
	for _, n := range batch {
		for _, fn := range subs {
			fn(n)
		}
	}
	return len(batch)
}

// take atomically detaches the pending queue and snapshots the
// subscribers, so delivery happens outside the bus lock.
func (b *Bus) take() ([]func(Notification), []Notification) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := make([]func(Notification), len(b.subs))
	copy(subs, b.subs)
	batch := b.queue
	b.queue = nil
	b.delivered += uint64(len(batch))
	return subs, batch
}

// Stats returns published/delivered/dropped totals.
func (b *Bus) Stats() (published, delivered, dropped uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.published, b.delivered, b.dropped
}
