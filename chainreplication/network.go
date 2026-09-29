package chainreplication

import (
	"sync"
	"time"
)

// MessageKind identifies a chain-replication protocol message.
type MessageKind int

const (
	// KindWrite propagates one write (seq, value) along the chain.
	KindWrite MessageKind = iota + 1
	// KindAck is a commit acknowledgment travelling back toward the head.
	KindAck
)

// Message is the unit of in-flight network traffic.
type Message struct {
	Kind MessageKind
	From string
	To   string
	Seq  int
	Val  string
}

// Network is the injectable transport. Implementations may delay, reorder and
// duplicate messages. FailNode must discard every message that the failed
// node sent or that is still in flight to/from it.
type Network interface {
	Send(m Message)
	FailNode(id string)
}

// DeliverFunc is called by a Network implementation when a message is
// injected back into the coordinator.
type DeliverFunc func(m Message)

// QueueNetwork is the deterministic test transport: Send enqueues the
// message, and the test drains it via Pending/Drop/Deliver.
type QueueNetwork struct {
	mu      sync.Mutex
	deliver DeliverFunc
	queue   []Message
	dead    map[string]bool
}

// NewQueueNetwork creates an empty queue transport.
func NewQueueNetwork(deliver DeliverFunc) *QueueNetwork {
	return &QueueNetwork{deliver: deliver}
}

// Bind sets the delivery callback (used when the coordinator is built after
// the transport).
func (n *QueueNetwork) Bind(deliver DeliverFunc) {
	n.deliver = deliver
}

// Send enqueues the message. Messages touching a known-dead node are dropped
// immediately, simulating loss of in-flight traffic on failure.
func (n *QueueNetwork) Send(m Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead[m.From] || n.dead[m.To] {
		return
	}
	n.queue = append(n.queue, m)
}

// FailNode drops every queued message sent by or addressed to the failed
// node and drops later traffic touching it.
func (n *QueueNetwork) FailNode(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead == nil {
		n.dead = map[string]bool{}
	}
	n.dead[id] = true
	kept := n.queue[:0]
	for _, m := range n.queue {
		if m.From != id && m.To != id {
			kept = append(kept, m)
		}
	}
	n.queue = kept
}

// Pending returns a copy of the queued messages in queue order.
func (n *QueueNetwork) Pending() []Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Message, len(n.queue))
	copy(out, n.queue)
	return out
}

// Deliver removes and injects the message at idx (0-based in Pending order).
func (n *QueueNetwork) Deliver(idx int) {
	n.mu.Lock()
	if idx < 0 || idx >= len(n.queue) {
		n.mu.Unlock()
		return
	}
	m := n.queue[idx]
	n.queue = append(n.queue[:idx], n.queue[idx+1:]...)
	n.mu.Unlock()
	n.deliver(m)
}

// DeliverKindSeq finds the first queued message matching kind and seq and
// delivers it; it reports whether one was found. Useful for reordering.
func (n *QueueNetwork) DeliverKindSeq(kind MessageKind, seq int) bool {
	for i, m := range n.Pending() {
		if m.Kind == kind && m.Seq == seq {
			n.Deliver(i)
			return true
		}
	}
	return false
}

// DropAll removes every queued message (simulates total in-flight loss).
func (n *QueueNetwork) DropAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.queue = nil
}

// Drop removes the message at idx without delivering it.
func (n *QueueNetwork) Drop(idx int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if idx < 0 || idx >= len(n.queue) {
		return
	}
	n.queue = append(n.queue[:idx], n.queue[idx+1:]...)
}

// AsyncNetwork delivers messages on background goroutines with optional
// duplication; used for concurrency/race tests.
type AsyncNetwork struct {
	mu      sync.Mutex
	wg      sync.WaitGroup
	deliver DeliverFunc
	dead    map[string]bool
	closed  bool
	minLag  time.Duration
	maxLag  time.Duration
	dupe    float64
}

// NewAsyncNetwork creates an asynchronous transport.
func NewAsyncNetwork(deliver DeliverFunc) *AsyncNetwork {
	return &AsyncNetwork{deliver: deliver}
}

// Bind sets the delivery callback.
func (n *AsyncNetwork) Bind(deliver DeliverFunc) { n.deliver = deliver }

// SetLag configures the random per-message latency range.
func (n *AsyncNetwork) SetLag(min, max time.Duration) *AsyncNetwork {
	n.minLag, n.maxLag = min, max
	return n
}

// SetDuplicate makes each sent message get delivered an extra copy with
// probability p (0..1), exercising duplicate handling.
func (n *AsyncNetwork) SetDuplicate(p float64) *AsyncNetwork {
	n.dupe = p
	return n
}

// Send schedules delivery after a random delay, with optional duplication.
func (n *AsyncNetwork) Send(m Message) {
	n.mu.Lock()
	if n.closed || n.dead[m.From] || n.dead[m.To] {
		n.mu.Unlock()
		return
	}
	n.wg.Add(1)
	n.mu.Unlock()
	go func() {
		defer n.wg.Done()
		n.sleepRandom()
		if n.aliveFor(m) {
			n.deliver(m)
		}
	}()
	if n.dupe > 0 && randFloat() < n.dupe {
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			n.sleepRandom()
			if n.aliveFor(m) {
				n.deliver(m)
			}
		}()
	}
}

// FailNode marks the node dead; late arrivals touching it are discarded.
func (n *AsyncNetwork) FailNode(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.dead == nil {
		n.dead = map[string]bool{}
	}
	n.dead[id] = true
}

// Close waits for all scheduled deliveries to finish.
func (n *AsyncNetwork) Close() {
	n.mu.Lock()
	n.closed = true
	n.mu.Unlock()
	n.wg.Wait()
}

func (n *AsyncNetwork) aliveFor(m Message) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return !n.closed && !n.dead[m.From] && !n.dead[m.To]
}

func (n *AsyncNetwork) sleepRandom() {
	if n.maxLag <= 0 {
		return
	}
	d := n.minLag
	if span := n.maxLag - n.minLag; span > 0 {
		d += time.Duration(randInt63n(int64(span)))
	}
	time.Sleep(d)
}
