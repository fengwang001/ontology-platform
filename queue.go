package ontology

import "sync"

// queue is a bounded, goroutine-safe message buffer for one subscription.
// push never blocks: when full, the configured FullPolicy is applied.
type queue struct {
	mu       sync.Mutex
	notify   chan struct{}
	items    []Message
	capacity int
	full     FullPolicy
	drain    DrainPolicy
	closed   bool
	dropped  uint64
	lastDrop uint64
}

func newQueue(capacity int, full FullPolicy, drain DrainPolicy) *queue {
	return &queue{
		notify:   make(chan struct{}, 1),
		capacity: capacity,
		full:     full,
		drain:    drain,
	}
}

// push delivers m unless the queue is closed or the full policy drops it.
// It reports whether the disconnect policy fired.
func (q *queue) push(m Message) (disconnected bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if len(q.items) == q.capacity {
		switch q.full {
		case FullDropNewest:
			q.recordDropLocked(m.Seq)
			return false
		case FullDropOldest:
			oldest := q.items[0]
			copy(q.items, q.items[1:])
			q.items = q.items[:len(q.items)-1]
			q.recordDropLocked(oldest.Seq)
		case FullDisconnect:
			q.recordDropLocked(m.Seq)
			q.closeLocked()
			return true
		}
	}
	q.items = append(q.items, m)
	q.signalLocked()
	return false
}

// pop blocks until a message is available or the queue is closed and
// drained.
func (q *queue) pop() (Message, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			m := q.items[0]
			q.items = q.items[1:]
			q.mu.Unlock()
			return m, true
		}
		if q.closed {
			q.mu.Unlock()
			return Message{}, false
		}
		q.mu.Unlock()
		<-q.notify
	}
}

// tryPop is the non-blocking variant of pop.
func (q *queue) tryPop() (Message, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return Message{}, false
	}
	m := q.items[0]
	q.items = q.items[1:]
	return m, true
}

// close terminates the queue. Queued items are kept or discarded
// according to the drain policy. It is idempotent.
func (q *queue) close() {
	q.mu.Lock()
	q.closeLocked()
	q.mu.Unlock()
}

func (q *queue) closeLocked() {
	if q.closed {
		return
	}
	q.closed = true
	if q.drain == DrainDiscard {
		q.items = nil
	}
	q.signalLocked()
}

// stats returns the total number of dropped messages and the sequence
// number of the most recent drop (0 if nothing was dropped).
func (q *queue) stats() (dropped uint64, lastDrop uint64) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped, q.lastDrop
}

func (q *queue) recordDropLocked(seq uint64) {
	q.dropped++
	q.lastDrop = seq
}

func (q *queue) signalLocked() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}
