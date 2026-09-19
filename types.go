package ontology

// OverflowPolicy decides what happens to a single subscriber whose bounded
// queue is full when a new message is fanned out. Policies are per
// subscription; one subscriber dropping never affects another subscriber.
type OverflowPolicy int

const (
	// DropOldest evicts the oldest queued message to make room for the new
	// one. The newest sequence is always delivered.
	DropOldest OverflowPolicy = iota
	// DropNewest discards the incoming message; the queued messages are
	// preserved.
	DropNewest
	// DropNewestAndDisconnect discards the incoming message, marks the
	// subscriber as lagging and removes it from the dispatcher. Messages
	// already in the queue can still be drained.
	DropNewestAndDisconnect
)

// CancelPolicy decides what happens to messages already queued but not yet
// received when a subscription is cancelled (or the dispatcher is closed).
type CancelPolicy int

const (
	// CancelDiscard drops all queued, undelivered messages immediately.
	CancelDiscard CancelPolicy = iota
	// CancelDrain keeps queued messages readable; Receive keeps returning
	// them and returns io.EOF once the queue is empty.
	CancelDrain
)

// Change is one published attribute change.
type Change struct {
	Entity    string
	Attribute string
	Value     any
}

// Message is a change stamped with the dispatcher-wide monotonically
// increasing sequence. Sequences are strictly increasing, never reused, and
// a subscriber may observe gaps when messages were dropped for it.
type Message struct {
	Seq       int64
	Entity    string
	Attribute string
	Value     any
}

// SubscribeOptions configures a subscription.
type SubscribeOptions struct {
	// EntityPrefix selects entities that equal the prefix or live below it
	// using dot-separated path boundaries ("user" matches "user" and
	// "user.1", never "superuser").
	EntityPrefix string
	// Attributes restricts which attributes match. An empty (or nil) set
	// matches every attribute of the matched entities; otherwise matches
	// require exact attribute name equality.
	Attributes []string
	// Buffer is the maximum number of undelivered messages queued per
	// subscriber.
	Buffer int
	// OnOverflow selects the full-queue behaviour.
	OnOverflow OverflowPolicy
	// OnCancel selects the queued-message behaviour after
	// Unsubscribe/Close.
	OnCancel CancelPolicy
}

// Delivery is what a subscriber receives.
type Delivery struct {
	Seq       int64
	Entity    string
	Attribute string
	Value     any
}

// MatchInfo describes a subscription for the deterministic
// Dispatcher.SubscribersFor query.
type MatchInfo struct {
	ID           uint64
	EntityPrefix string
	Attributes   []string
}
