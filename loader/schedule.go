package loader

// Internal scheduling core. All methods in this file require s.mu to be held
// and return events to be dispatched after the lock is released.

func (s *Scheduler) event(kind EventKind, r *Request) Event {
	return Event{
		Time:     s.now,
		Kind:     kind,
		ID:       r.id,
		Origin:   r.origin,
		URL:      r.url,
		Priority: r.prio,
		Progress: r.progress,
	}
}

// expireLocked moves preload cache entries older than the TTL to the waste
// report and returns the corresponding events.
func (s *Scheduler) expireLocked() []Event {
	return s.cache.expire(s.now)
}

func (s *Scheduler) freeSlotLocked(r *Request) {
	delete(s.inFlightByOrigin[r.origin], r.id)
	if len(s.inFlightByOrigin[r.origin]) == 0 {
		delete(s.inFlightByOrigin, r.origin)
	}
	s.inFlightPerOrig[r.origin]--
	if s.inFlightPerOrig[r.origin] == 0 {
		delete(s.inFlightPerOrig, r.origin)
	}
	s.inFlightTotal--
}

// preemptLocked lets a pending highest-priority request displace the
// lowest-priority, latest-started pausable in-flight request of the same
// origin. Only PriorityHighest may preempt.
func (s *Scheduler) preemptLocked(r *Request) []Event {
	if r.state != StatePending || r.prio != PriorityHighest {
		return nil
	}
	if s.inFlightTotal < s.cfg.GlobalLimit && s.inFlightPerOrig[r.origin] < s.cfg.PerOriginLimit {
		return nil
	}
	var victim *Request
	for _, c := range s.inFlightByOrigin[r.origin] {
		if c.prio >= PriorityHighest || c.pauses >= s.cfg.MaxPauses {
			continue
		}
		if victim == nil || c.prio < victim.prio ||
			(c.prio == victim.prio && c.startSeq > victim.startSeq) {
			victim = c
		}
	}
	if victim == nil {
		return nil
	}
	return s.pauseLocked(victim)
}

// pauseLocked returns an in-flight request to the pending queue, keeping its
// transferred progress and its original registration order.
func (s *Scheduler) pauseLocked(v *Request) []Event {
	v.pauses++
	v.state = StatePending
	s.freeSlotLocked(v)
	s.pending.insert(v)
	s.pendN++
	return []Event{s.event(EventPaused, v)}
}

// scheduleLocked starts pending requests while quotas allow, highest
// priority first and, within a priority, by registration order.
func (s *Scheduler) scheduleLocked() []Event {
	var evs []Event
	for s.inFlightTotal < s.cfg.GlobalLimit {
		r := s.pending.selectNext(func(o Origin) bool {
			return s.inFlightPerOrig[o] >= s.cfg.PerOriginLimit
		})
		if r == nil {
			break
		}
		s.pending.dequeue(r)
		s.pendN--
		r.state = StateInFlight
		s.startSeq++
		r.startSeq = s.startSeq
		if s.inFlightByOrigin[r.origin] == nil {
			s.inFlightByOrigin[r.origin] = make(map[uint64]*Request)
		}
		s.inFlightByOrigin[r.origin][r.id] = r
		s.inFlightPerOrig[r.origin]++
		s.inFlightTotal++
		kind := EventStarted
		if r.pauses > 0 {
			kind = EventResumed
		}
		evs = append(evs, s.event(kind, r))
		if r.size == 0 {
			evs = append(evs, s.completeLocked(r)...)
		}
	}
	return evs
}

// completeLocked finishes an in-flight request: a preload enters the cache,
// attachers complete from it, and freed quota is refilled.
func (s *Scheduler) completeLocked(r *Request) []Event {
	r.state = StateDone
	s.freeSlotLocked(r)
	var evs []Event
	if r.typ == TypePreload {
		k := r.key()
		delete(s.preloads, k)
		s.cache.put(k, &cacheEntry{
			as:          r.as,
			cred:        r.cred,
			integrity:   r.integrity,
			completedAt: s.now,
		})
	}
	for _, a := range r.attachers {
		a.state = StateDone
		a.fromCache = true
		a.attachTo = nil
		evs = append(evs, s.event(EventCompleted, a))
	}
	r.attachers = nil
	evs = append(evs, s.event(EventCompleted, r))
	return append(evs, s.scheduleLocked()...)
}

// failLocked terminates an in-flight request as failed; attachers of a
// preload fail together with it.
func (s *Scheduler) failLocked(r *Request) []Event {
	r.state = StateFailed
	s.freeSlotLocked(r)
	var evs []Event
	if r.typ == TypePreload {
		delete(s.preloads, r.key())
	}
	for _, a := range r.attachers {
		a.state = StateFailed
		a.attachTo = nil
		evs = append(evs, s.event(EventFailed, a))
	}
	r.attachers = nil
	evs = append(evs, s.event(EventFailed, r))
	return append(evs, s.scheduleLocked()...)
}
