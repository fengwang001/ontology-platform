package loader

// Mutating operations. Rejection precedence is: invalid argument, then clock
// rewind, then unknown request, then illegal state. A rejected operation
// changes nothing: no quota, cache, progress or clock is touched.

// Register validates in, assigns an id and either starts, queues, attaches
// or immediately completes the request from the preload cache.
func (s *Scheduler) Register(at int64, in RequestInput) (uint64, error) {
	if in.URL == "" {
		return 0, errInvalid("empty url")
	}
	if !in.Type.valid() {
		return 0, errInvalid("unknown resource type")
	}
	if !in.Priority.valid() {
		return 0, errInvalid("priority out of range")
	}
	if in.Type == TypePreload && (!in.As.valid() || in.As == TypePreload) {
		return 0, errInvalid("preload must declare a non-preload resource type")
	}
	if in.Size < 0 {
		return 0, errInvalid("negative size")
	}
	key := cacheKey{origin: in.Origin, url: in.URL}

	s.mu.Lock()
	if in.Type == TypePreload {
		if _, dup := s.preloads[key]; dup || s.cache.has(key) {
			s.mu.Unlock()
			return 0, errInvalid("duplicate preload registration")
		}
	}
	if at < s.now {
		s.mu.Unlock()
		return 0, errRewind("timestamp before current clock")
	}
	s.now = at
	evs := s.expireLocked()

	s.seq++
	s.nextID++
	as := in.Type
	if in.Type == TypePreload {
		as = in.As
	}
	r := &Request{
		id:        s.nextID,
		origin:    in.Origin,
		url:       in.URL,
		typ:       in.Type,
		as:        as,
		prio:      in.Priority,
		cred:      in.Credentials,
		integrity: in.Integrity,
		size:      in.Size,
		seq:       s.seq,
		state:     StatePending,
	}
	s.reqs[r.id] = r
	evs = append(evs, s.event(EventRegistered, r))

	if in.Type != TypePreload {
		if p, ok := s.preloads[key]; ok && preloadMatches(p, in) {
			r.state = StateAttached
			r.attachTo = p
			p.attachers = append(p.attachers, r)
			att := s.event(EventAttached, r)
			att.Target = p.id
			evs = append(evs, att)
			if in.Priority > p.prio {
				p.prio = in.Priority
				if p.state == StatePending {
					s.pending.insert(p)
					evs = append(evs, s.preemptLocked(p)...)
				}
			}
			evs = append(evs, s.scheduleLocked()...)
			s.commit(evs)
			return r.id, nil
		}
		if e := s.cache.get(key); e != nil && e.matches(in.Type, in.Credentials, in.Integrity) {
			s.cache.delete(key)
			r.state = StateDone
			r.fromCache = true
			evs = append(evs, s.event(EventCacheHit, r), s.event(EventCompleted, r))
			s.commit(evs)
			return r.id, nil
		}
	}

	s.pending.insert(r)
	s.pendN++
	if in.Type == TypePreload {
		s.preloads[key] = r
	}
	evs = append(evs, s.preemptLocked(r)...)
	evs = append(evs, s.scheduleLocked()...)
	s.commit(evs)
	return r.id, nil
}

func preloadMatches(p *Request, in RequestInput) bool {
	return p.as == in.Type && p.cred == in.Credentials && p.integrity == in.Integrity
}

// lookup validates clock, existence and non-terminal state in rejection
// precedence order. The caller holds s.mu.
func (s *Scheduler) lookup(at int64, id uint64) (*Request, error) {
	if at < s.now {
		return nil, errRewind("timestamp before current clock")
	}
	r, ok := s.reqs[id]
	if !ok {
		return nil, errNotFound("unknown request id")
	}
	if r.state.Terminal() {
		return nil, errState("request already in a terminal state")
	}
	return r, nil
}

// Cancel aborts a pending, in-flight or attached request. Aborting a preload
// fails its attachers with ErrAborted.
func (s *Scheduler) Cancel(at int64, id uint64) error {
	if id == 0 {
		return errInvalid("zero request id")
	}
	s.mu.Lock()
	r, err := s.lookup(at, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.now = at
	evs := s.expireLocked()

	switch r.state {
	case StatePending:
		r.queued = false
		s.pendN--
		r.state = StateAborted
		if r.typ == TypePreload {
			delete(s.preloads, r.key())
		}
		evs = append(evs, s.event(EventAborted, r))
	case StateInFlight:
		r.state = StateAborted
		s.freeSlotLocked(r)
		if r.typ == TypePreload {
			delete(s.preloads, r.key())
		}
		for _, a := range r.attachers {
			a.state = StateFailed
			a.hasErr = true
			a.errKind = ErrAborted
			a.attachTo = nil
			evs = append(evs, s.event(EventFailed, a))
		}
		r.attachers = nil
		evs = append(evs, s.event(EventAborted, r))
		evs = append(evs, s.scheduleLocked()...)
	case StateAttached:
		t := r.attachTo
		for i, a := range t.attachers {
			if a == r {
				t.attachers = append(t.attachers[:i], t.attachers[i+1:]...)
				break
			}
		}
		r.attachTo = nil
		r.state = StateAborted
		evs = append(evs, s.event(EventAborted, r))
	}
	s.commit(evs)
	return nil
}

// Progress advances an in-flight request by n bytes; reaching its size
// completes it.
func (s *Scheduler) Progress(at int64, id uint64, n int64) error {
	if id == 0 {
		return errInvalid("zero request id")
	}
	if n < 0 {
		return errInvalid("negative progress")
	}
	s.mu.Lock()
	r, err := s.lookup(at, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if r.state != StateInFlight {
		s.mu.Unlock()
		return errState("request is not in flight")
	}
	s.now = at
	evs := s.expireLocked()

	r.progress += n
	if r.progress >= r.size {
		evs = append(evs, s.completeLocked(r)...)
	}
	s.commit(evs)
	return nil
}

// Fail terminates an in-flight request as failed and frees its quota.
func (s *Scheduler) Fail(at int64, id uint64) error {
	if id == 0 {
		return errInvalid("zero request id")
	}
	s.mu.Lock()
	r, err := s.lookup(at, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if r.state != StateInFlight {
		s.mu.Unlock()
		return errState("request is not in flight")
	}
	s.now = at
	evs := s.expireLocked()
	evs = append(evs, s.failLocked(r)...)
	s.commit(evs)
	return nil
}

// AdvanceClock moves the logical clock forward and expires wasted preloads.
func (s *Scheduler) AdvanceClock(at int64) error {
	s.mu.Lock()
	if at < s.now {
		s.mu.Unlock()
		return errRewind("timestamp before current clock")
	}
	s.now = at
	evs := s.expireLocked()
	s.commit(evs)
	return nil
}
