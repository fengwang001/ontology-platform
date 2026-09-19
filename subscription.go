package ontology

import "context"

// Subscription is a consumer handle returned by Dispatcher.Subscribe. It is
// safe for concurrent use; typically a single goroutine calls Next while
// others call Unsubscribe or Stats.
type Subscription struct {
	d *Dispatcher
	s *subscriber
}

// ID returns the dispatcher-unique, monotonically assigned subscription id.
// Targets lists ids in ascending order.
func (sub *Subscription) ID() int64 { return sub.s.id }

// Next blocks until the next in-order event is available, the subscription
// terminates, or ctx is done. Sequence numbers returned to one subscriber
// are strictly increasing; gaps correspond exactly to Dropped events.
//
// After termination it drains any buffered events first, then returns one
// of ErrDisconnected, ErrDrained, or ErrSubscriptionGone. Context
// cancellation returns ctx.Err() without consuming an event.
func (sub *Subscription) Next(ctx context.Context) (Event, error) {
	for {
		sub.d.mu.Lock()
		if ev, ok := sub.s.queue.pop(); ok {
			sub.d.mu.Unlock()
			return ev, nil
		}
		if err := sub.s.terminalError(); err != nil {
			sub.d.mu.Unlock()
			return Event{}, err
		}
		// Install a fresh wake-up channel while holding the lock, then wait
		// outside it. Any offer that happens after this point closes this
		// channel, so a wait cannot miss a signal.
		wake := sub.s.notify
		sub.d.mu.Unlock()

		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-wake:
		}
	}
}

// Unsubscribe detaches the subscription from future fan-out. It is idempotent:
// repeated calls are no-ops and never panic or error. Buffered events are
// handled per DrainOnUnsubscribe: either left for Next to drain or discarded
// immediately. Unsubscribing during a concurrent Publish never makes that
// Publish fail and never affects other subscribers.
func (sub *Subscription) Unsubscribe() {
	sub.d.unsubscribeID(sub.s.id)
}

// Stats returns a point-in-time snapshot of this subscriber's counters.
func (sub *Subscription) Stats() SubscriptionStats {
	sub.d.mu.Lock()
	defer sub.d.mu.Unlock()
	return SubscriptionStats{
		Enqueued:    sub.s.enqueued,
		Delivered:   sub.s.offered,
		Dropped:     sub.s.dropped,
		LastDropSeq: sub.s.lastDrop,
		Closed:      !sub.s.active,
		Pending:     sub.s.queue.len(),
	}
}
