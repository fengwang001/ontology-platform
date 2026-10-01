// Package delivery implements a channel unacked-delivery ledger with a
// prefetch limit, batch ack/reject by delivery tag, and requeue repositioning
// by enqueue sequence number.
package delivery

import (
	"errors"
	"sync"
)

var (
	// ErrInvalidTag is returned when the tag is 0 or greater than the
	// maximum tag ever assigned.
	ErrInvalidTag = errors.New("delivery: invalid tag")
	// ErrAlreadySettled is returned when a non-batch operation names a tag
	// that has already been settled (acked, dropped or requeued).
	ErrAlreadySettled = errors.New("delivery: tag already settled")
	// ErrEmptyRange is returned when a batch operation's tag range
	// contains no unacked deliveries.
	ErrEmptyRange = errors.New("delivery: no unacked delivery in range")
	// ErrPrefetchFull is returned by Deliver when the number of unacked
	// deliveries has reached the prefetch limit. It takes precedence over
	// ErrQueueEmpty when both conditions hold.
	ErrPrefetchFull = errors.New("delivery: prefetch limit reached")
	// ErrQueueEmpty is returned by Deliver when the queue holds no message.
	ErrQueueEmpty = errors.New("delivery: queue is empty")
)

// Delivery describes one dispatched message.
type Delivery struct {
	Tag         uint64 // delivery tag, monotonically increasing, never reused
	Seq         uint64 // enqueue sequence number of the message
	Payload     any
	Redelivered bool // false on first delivery, true after a requeue
}

// Stats is a point-in-time view of the ledger counters.
type Stats struct {
	Prefetch int    // configured prefetch limit P
	Queued   int    // messages waiting in the queue
	Unacked  int    // delivered but not yet settled
	Dropped  uint64 // messages discarded by reject-without-requeue
	MaxSeq   uint64 // highest enqueue sequence number assigned
	MaxTag   uint64 // highest delivery tag assigned
}

// Snapshot is a consistent view of queue and unacked contents, for queries
// and invariant checks.
type Snapshot struct {
	QueueSeqs   []uint64          // queue contents in dequeue order (seq asc)
	UnackedSeqs map[uint64]uint64 // tag -> seq
}

type queuedMessage struct {
	seq      uint64
	payload  any
	requeued bool
}

// Ledger tracks queued, unacked and settled messages for one channel.
// All methods are safe for concurrent use; the result is equivalent to
// some serial order of the calls.
type Ledger struct {
	mu       sync.Mutex
	prefetch int
	nextSeq  uint64
	nextTag  uint64
	queue    []queuedMessage // sorted by seq ascending
	unacked  map[uint64]queuedMessage
	dropped  uint64
}

// New creates a Ledger with the given prefetch limit. It panics if
// prefetch is less than 1.
func New(prefetch int) *Ledger {
	if prefetch < 1 {
		panic("delivery: prefetch must be >= 1")
	}
	return &Ledger{
		prefetch: prefetch,
		nextSeq:  1,
		nextTag:  1,
		unacked:  make(map[uint64]queuedMessage),
	}
}

// Enqueue assigns the next enqueue sequence number (starting at 1, never
// reused) to payload and places it in the queue. It returns the sequence
// number.
func (l *Ledger) Enqueue(payload any) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	seq := l.nextSeq
	l.nextSeq++
	l.queue = insertBySeq(l.queue, queuedMessage{seq: seq, payload: payload})
	return seq
}

// Deliver takes the queued message with the smallest enqueue sequence
// number, assigns it the next delivery tag and marks it unacked. It fails
// with ErrPrefetchFull when the unacked count has reached the prefetch
// limit (checked first, even if the queue is also empty), and with
// ErrQueueEmpty when there is nothing to deliver.
func (l *Ledger) Deliver() (Delivery, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.unacked) >= l.prefetch {
		return Delivery{}, ErrPrefetchFull
	}
	if len(l.queue) == 0 {
		return Delivery{}, ErrQueueEmpty
	}
	m := l.queue[0]
	l.queue = l.queue[1:]
	tag := l.nextTag
	l.nextTag++
	l.unacked[tag] = m
	return Delivery{Tag: tag, Seq: m.seq, Payload: m.payload, Redelivered: m.requeued}, nil
}

// Ack settles deliveries by tag. With multiple=false only the delivery
// carrying tag is settled; with multiple=true every unacked delivery whose
// tag is less than or equal to tag is settled. Settled messages are removed
// permanently. It returns the number of settled deliveries.
func (l *Ledger) Ack(tag uint64, multiple bool) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	tags, err := l.rangeTags(tag, multiple)
	if err != nil {
		return 0, err
	}
	for _, t := range tags {
		delete(l.unacked, t)
	}
	return len(tags), nil
}

// Reject settles deliveries by tag, using the same range rule as Ack. When
// requeue is true the affected messages go back into the queue, each
// inserted before the first queued message with a larger enqueue sequence
// number; when requeue is false they are discarded and counted as dropped.
// It returns the number of affected deliveries.
func (l *Ledger) Reject(tag uint64, multiple bool, requeue bool) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	tags, err := l.rangeTags(tag, multiple)
	if err != nil {
		return 0, err
	}
	for _, t := range tags {
		m := l.unacked[t]
		delete(l.unacked, t)
		if requeue {
			m.requeued = true
			l.queue = insertBySeq(l.queue, m)
		} else {
			l.dropped++
		}
	}
	return len(tags), nil
}

// rangeTags validates the operation and returns the unacked tags in scope,
// in ascending tag order. The caller must hold l.mu.
func (l *Ledger) rangeTags(tag uint64, multiple bool) ([]uint64, error) {
	if tag == 0 || tag >= l.nextTag {
		return nil, ErrInvalidTag
	}
	if !multiple {
		if _, ok := l.unacked[tag]; !ok {
			return nil, ErrAlreadySettled
		}
		return []uint64{tag}, nil
	}
	var tags []uint64
	for t := range l.unacked {
		if t <= tag {
			tags = append(tags, t)
		}
	}
	if len(tags) == 0 {
		return nil, ErrEmptyRange
	}
	sortUint64s(tags)
	return tags, nil
}

// Stats returns a point-in-time view of the ledger counters.
func (l *Ledger) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Stats{
		Prefetch: l.prefetch,
		Queued:   len(l.queue),
		Unacked:  len(l.unacked),
		Dropped:  l.dropped,
		MaxSeq:   l.nextSeq - 1,
		MaxTag:   l.nextTag - 1,
	}
}

// Snapshot returns a consistent view of the queue contents (in dequeue
// order) and the unacked set (tag -> seq).
func (l *Ledger) Snapshot() Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	snap := Snapshot{UnackedSeqs: make(map[uint64]uint64, len(l.unacked))}
	for _, m := range l.queue {
		snap.QueueSeqs = append(snap.QueueSeqs, m.seq)
	}
	for tag, m := range l.unacked {
		snap.UnackedSeqs[tag] = m.seq
	}
	return snap
}

// insertBySeq inserts m before the first queued message with a larger
// sequence number, keeping the queue sorted by seq ascending.
func insertBySeq(queue []queuedMessage, m queuedMessage) []queuedMessage {
	i := 0
	for i < len(queue) && queue[i].seq < m.seq {
		i++
	}
	queue = append(queue, queuedMessage{})
	copy(queue[i+1:], queue[i:])
	queue[i] = m
	return queue
}

func sortUint64s(v []uint64) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}
