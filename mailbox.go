package ontology

// putOutcome reports what happened to one offered message.
type putOutcome int

const (
	putEnqueued putOutcome = iota
	putDroppedNew
	putDisconnect
)

// mailbox is a subscriber-owned bounded FIFO of messages. Every method is
// guarded by mu, including the internal signal channel, so a slow consumer
// only ever blocks inside Get (never blocks a fan-out Put) and one mailbox
// cannot interfere with another.
type mailbox struct {
	mu       chan struct{} // serves as the mutex: cap 1, holds the token
	signal   chan struct{} // closed to wake the current Get; replaced each notify
	msgs     []Delivery
	capacity int
	overflow OverflowPolicy
	ended    bool
}

func newMailbox(capacity int, overflow OverflowPolicy) *mailbox {
	m := &mailbox{
		mu:       make(chan struct{}, 1),
		signal:   make(chan struct{}),
		capacity: capacity,
		overflow: overflow,
	}
	m.mu <- struct{}{}
	return m
}

func (m *mailbox) lock()   { <-m.mu }
func (m *mailbox) unlock() { m.mu <- struct{}{} }

// notify wakes the current waiter. Callers must hold m.lock(). The captured
// channel pattern means every wait observes only signals created after it
// began, so stale notifications can never be consumed as a lost wakeup.
func (m *mailbox) notify() { close(m.signal); m.signal = make(chan struct{}) }

// Put offers one message. The returned droppedSeq is non-zero exactly when a
// message was dropped: for DropOldest it is the evicted message's sequence;
// for DropNewest and disconnect it is the incoming message's sequence.
func (m *mailbox) Put(d Delivery) (outcome putOutcome, droppedSeq int64) {
	m.lock()
	defer m.unlock()
	if m.ended {
		return putDroppedNew, d.Seq
	}
	if len(m.msgs) < m.capacity {
		m.msgs = append(m.msgs, d)
		m.notify()
		return putEnqueued, 0
	}
	switch m.overflow {
	case DropOldest:
		dropped := m.msgs[0]
		m.msgs = m.msgs[1:]
		m.msgs = append(m.msgs, d)
		m.notify()
		return putDroppedNew, dropped.Seq
	case DropNewestAndDisconnect:
		m.ended = true
		m.notify()
		return putDisconnect, d.Seq
	default: // DropNewest
		return putDroppedNew, d.Seq
	}
}

// Get blocks until a message is available, returning it and ok=true. After
// the mailbox ends and drains, ok=false is returned.
func (m *mailbox) Get() (Delivery, bool) {
	for {
		m.lock()
		if len(m.msgs) > 0 {
			d := m.msgs[0]
			m.msgs = m.msgs[1:]
			m.unlock()
			return d, true
		}
		if m.ended {
			m.unlock()
			return Delivery{}, false
		}
		wait := m.signal
		m.unlock()
		<-wait
	}
}

// Terminate ends the mailbox. With CancelDiscard queued messages are removed
// immediately; with CancelDrain they stay readable until consumed.
func (m *mailbox) Terminate(policy CancelPolicy) {
	m.lock()
	defer m.unlock()
	if m.ended {
		return
	}
	m.ended = true
	if policy == CancelDiscard {
		m.msgs = nil
	}
	m.notify()
}
