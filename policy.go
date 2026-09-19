package ontology

// FullPolicy decides what happens when a subscriber's bounded queue is
// full at Publish time. Publish never blocks; the policy is applied
// immediately and independently per subscriber.
type FullPolicy int

const (
	// FullDropOldest evicts the oldest queued message to make room for
	// the new one. The evicted message counts as dropped.
	FullDropOldest FullPolicy = iota
	// FullDropNewest refuses the incoming message. It counts as dropped.
	FullDropNewest
	// FullDisconnect drops the incoming message, marks the subscriber
	// as lagging and disconnects it: it receives no further messages.
	FullDisconnect
)

func (p FullPolicy) String() string {
	switch p {
	case FullDropOldest:
		return "drop-oldest"
	case FullDropNewest:
		return "drop-newest"
	case FullDisconnect:
		return "disconnect"
	default:
		return "unknown"
	}
}

// DrainPolicy decides what happens to messages still queued when a
// subscription ends (Cancel, disconnect, or dispatcher Close).
type DrainPolicy int

const (
	// DrainRead leaves queued messages readable until exhausted.
	DrainRead DrainPolicy = iota
	// DrainDiscard discards queued messages immediately.
	DrainDiscard
)

func (p DrainPolicy) String() string {
	switch p {
	case DrainRead:
		return "read"
	case DrainDiscard:
		return "discard"
	default:
		return "unknown"
	}
}
