
package fanout

// forward owns outCh for the lifetime of the subscription. It either
// forwards the bounded input queue to the subscriber, or performs one of
// the two wind-down protocols signaled on drain/purge.
func (s *Subscription) forward() {
	for {
		select {
		case m, ok := <-s.inCh:
			if !ok {
				close(s.outCh)
				return
			}
			select {
			case s.outCh <- m:
			case <-s.purge:
				s.abandon(m)
				close(s.outCh)
				return
			}
		case <-s.drain:
			s.drainQueue()
			close(s.outCh)
			return
		case <-s.purge:
			s.abandonQueue()
			close(s.outCh)
			return
		}
	}
}

// drainQueue forwards everything still buffered, then returns. New messages
// can no longer arrive because the canceled flag is set before signaling.
func (s *Subscription) drainQueue() {
	for {
		select {
		case m := <-s.inCh:
			s.outCh <- m
		default:
			return
		}
	}
}

// abandon is abandonQueue with one message already dequeued for sending.
func (s *Subscription) abandon(inFlight Envelope) {
	s.subMu.Lock()
	s.recordDropLocked(inFlight.Seq)
	s.subMu.Unlock()
	s.abandonQueue()
}

// abandonQueue discards every buffered message, counting each as dropped.
func (s *Subscription) abandonQueue() {
	s.subMu.Lock()
	for {
		select {
		case m := <-s.inCh:
			s.recordDropLocked(m.Seq)
		default:
			s.subMu.Unlock()
			return
		}
	}
}

// deliver enqueues one envelope. It never blocks the publisher: a full queue
// is resolved immediately according to the subscriber's own drop policy.
func (s *Subscription) deliver(env Envelope) {
	s.subMu.Lock()
	if s.detached {
		s.recordDropLocked(env.Seq)
		s.subMu.Unlock()
		return
	}
	if s.canceled {
		s.subMu.Unlock()
		return
	}
	select {
	case s.inCh <- env:
		s.subMu.Unlock()
	default:
		s.handleFullLocked(env)
		s.subMu.Unlock()
	}
}

func (s *Subscription) handleFullLocked(env Envelope) {
	switch s.drop {
	case DropNewest:
		s.recordDropLocked(env.Seq)
	case DropOldest:
		select {
		case old := <-s.inCh:
			s.recordDropLocked(old.Seq)
			select {
			case s.inCh <- env:
			default:
				s.recordDropLocked(env.Seq)
			}
		default:
			s.recordDropLocked(env.Seq)
		}
	case DropDisconnect:
		s.detached = true
		s.canceled = true
		s.shut = true
		s.recordDropLocked(env.Seq)
		if s.onCancel == CancelPurge {
			close(s.purge)
		} else {
			close(s.drain)
		}
	}
}

func (s *Subscription) recordDropLocked(seq uint64) {
	s.droppedCount++
	s.lastDropSeq = seq
}

// Stats returns a point-in-time copy of the subscriber's drop counters.
func (s *Subscription) Stats() Stats {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	return Stats{
		Dropped:        s.droppedCount,
		LastDroppedSeq: s.lastDropSeq,
		Detached:       s.detached,
	}
}

// shutdown winds the subscription down once. It is idempotent.
func (s *Subscription) shutdown() {
	s.subMu.Lock()
	if s.canceled && s.shut {
		s.subMu.Unlock()
		return
	}
	s.canceled = true
	s.shut = true
	purge := s.onCancel == CancelPurge
	s.subMu.Unlock()
	if purge {
		close(s.purge)
	} else {
		close(s.drain)
	}
}
