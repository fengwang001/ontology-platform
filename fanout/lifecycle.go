
package fanout

// Unsubscribe removes the subscription. It is idempotent: unsubscribing
// twice (or unsubscribing after Close) never panics and never returns an
// error.
//
// After it returns, the subscriber receives no further messages. The queue
// is treated per the subscription's CancelPolicy: CancelDrain leaves
// buffered messages readable until consumed (then C() closes), CancelPurge
// discards them and closes C() promptly. Unsubscribing a subscription while
// a Publish is fanning out cannot fail that publish or affect any other
// subscriber; topology changes are serialized against the fan-out.
func (d *Dispatcher) Unsubscribe(sub *Subscription) {
	if sub == nil {
		return
	}
	d.mu.Lock()
	_, ok := d.subs[sub.id]
	if ok {
		delete(d.subs, sub.id)
	}
	d.mu.Unlock()
	sub.shutdown()
}

// Close shuts the dispatcher down. Semantics:
//
//   - after Close, Publish returns ErrDispatcherClosed and delivers nothing.
//   - Subscribe returns ErrDispatcherClosed.
//   - every subscription queue is finalized per its CancelPolicy.
//   - a Publish already in progress completes atomically: the write lock
//     cannot be acquired (nor observed closed) while a fan-out holds the
//     read lock, so no publish is delivered to only part of its targets.
//   - Close is idempotent.
func (d *Dispatcher) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	subs := d.subs
	d.subs = make(map[uint64]*Subscription)
	d.mu.Unlock()

	for _, sub := range subs {
		sub.shutdown()
	}
}
