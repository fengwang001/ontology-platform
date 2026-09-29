package chainreplication

import (
	"fmt"
	"sync"
)

// TestNetwork is an injected Network that keeps messages in an explicit
// queue until a test delivers them. It supports delay (messages simply stay
// queued), arbitrary reordering, duplication and deterministic replay:
// Send/Deliver sequences executed in the same order give identical results.
type TestNetwork struct {
	mu      sync.Mutex
	logger  Logger
	queue   []Message
	dropped []Message
	sent    []Message
}

func NewTestNetwork(logger Logger) *TestNetwork {
	if logger == nil {
		logger = discardLogger{}
	}
	return &TestNetwork{logger: logger}
}

func (n *TestNetwork) Send(m Message) {
	n.mu.Lock()
	n.queue = append(n.queue, m)
	n.sent = append(n.sent, m)
	n.logger.Logf("NETWORK send %s seq=%d from=%s to=%s (queued, pending=%d)",
		msgTypeName(m.Type), m.Seq, m.From, m.To, len(n.queue))
	n.mu.Unlock()
}

// DropNode discards queued messages sent by or addressed to a failed node.
func (n *TestNetwork) DropNode(id string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	kept := n.queue[:0]
	for _, m := range n.queue {
		if m.From == id || m.To == id {
			n.dropped = append(n.dropped, m)
			n.logger.Logf("NETWORK drop %s seq=%d from=%s to=%s (involves failed node=%s)",
				msgTypeName(m.Type), m.Seq, m.From, m.To, id)
			continue
		}
		kept = append(kept, m)
	}
	n.queue = kept
}

// Pending reports how many messages are queued but not yet delivered.
func (n *TestNetwork) Pending() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.queue)
}

// Peek returns a copy of the queued messages without delivering them.
func (n *TestNetwork) Peek() []Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Message(nil), n.queue...)
}

// DeliverOne delivers the head-of-queue message. It returns false when empty.
func (n *TestNetwork) DeliverOne(c *Coordinator) bool {
	return n.DeliverAt(c, 0)
}

// DeliverAt delivers the queued message at index i, enabling reordering.
func (n *TestNetwork) DeliverAt(c *Coordinator, i int) bool {
	n.mu.Lock()
	if i < 0 || i >= len(n.queue) {
		n.mu.Unlock()
		return false
	}
	m := n.queue[i]
	n.queue = append(n.queue[:i], n.queue[i+1:]...)
	n.mu.Unlock()
	c.Deliver(m)
	return true
}

// Duplicate enqueues another copy of the queued message at index i.
func (n *TestNetwork) Duplicate(i int) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if i < 0 || i >= len(n.queue) {
		return fmt.Errorf("duplicate: index %d out of range (pending=%d)", i, len(n.queue))
	}
	m := n.queue[i]
	n.queue = append(n.queue, m)
	n.logger.Logf("NETWORK duplicate %s seq=%d from=%s to=%s", msgTypeName(m.Type), m.Seq, m.From, m.To)
	return nil
}

// DeliverAll drains the queue in FIFO order, repeatedly, until empty.
func (n *TestNetwork) DeliverAll(c *Coordinator) {
	for {
		n.mu.Lock()
		if len(n.queue) == 0 {
			n.mu.Unlock()
			return
		}
		m := n.queue[0]
		n.queue = n.queue[1:]
		n.mu.Unlock()
		c.Deliver(m)
	}
}

// Deliver drains a specific message matching from/to/type/seq (test helper).
func (n *TestNetwork) Deliver(c *Coordinator, from, to string, t MessageType, seq int) bool {
	n.mu.Lock()
	for i, m := range n.queue {
		if m.From == from && m.To == to && m.Type == t && m.Seq == seq {
			n.queue = append(n.queue[:i], n.queue[i+1:]...)
			n.mu.Unlock()
			c.Deliver(m)
			return true
		}
	}
	n.mu.Unlock()
	return false
}

// Dropped returns messages removed by DropNode.
func (n *TestNetwork) Dropped() []Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]Message(nil), n.dropped...)
}
