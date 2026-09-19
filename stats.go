package ontology

// SubscriptionStats attributes dropped events to a single subscriber.
type SubscriptionStats struct {
	// Enqueued is the total number of events successfully placed in the queue
	// (and therefore eventually observable, in order, by the consumer).
	Enqueued int64
	// Delivered is the total number of events offered to this subscriber,
	// i.e. matched events the dispatcher attempted to fan out.
	Delivered int64
	// Dropped is the cumulative number of events lost to overflow.
	Dropped int64
	// LastDropSeq is the publisher-global sequence number involved in the
	// most recent drop. For DropOldest it is the seq of the evicted oldest
	// event; for DropNewest/Disconnect it is the seq of the rejected event.
	// Zero when no drop ever happened.
	LastDropSeq int64
	// Closed reports whether the subscription is detached from fan-out
	// (unsubscribed, disconnected, or dispatcher closed).
	Closed bool
	// Pending is the number of events currently buffered and not yet read.
	Pending int
}
