package traffic

import "sync"

// OpLog receives a line per accepted mutating operation. It is intended
// for the differential tests; a nil logger disables logging.
type OpLog func(line string)

// Service is the concurrency-safe traffic incident simulation service.
// All methods take the service-wide mutex, so every concurrent history
// is equivalent to some serial order, and a fixed serial replay
// reproduces identical trajectories.
type Service struct {
	mu     sync.Mutex
	eng    *engine
	logger OpLog
}

// NewService constructs a service over a frozen network snapshot with the
// given per-vehicle length.
func NewService(n *Network, vehicleLength *Rat) *Service {
	if n == nil || vehicleLength == nil || vehicleLength.Sign() <= 0 {
		panic("invalid service construction arguments")
	}
	n.freeze()
	return &Service{eng: newEngine(n, vehicleLength)}
}

// SetLogger attaches an operation log (pass nil to disable).
func (s *Service) SetLogger(l OpLog) {
	s.mu.Lock()
	s.logger = l
	s.mu.Unlock()
}

func (s *Service) log(line string) {
	if s.logger != nil {
		s.logger(line)
	}
}

// Now returns the current simulation time.
func (s *Service) Now() *Rat {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ratCopy(s.eng.now)
}

// validRatio reports r in [0,1].
func validRatio(r *Rat) bool {
	return r != nil && r.Sign() >= 0 && r.Cmp(newRat().SetInt64(1)) <= 0
}

// Register adds an incident. Its timestamp is the incident start time,
// which must not be earlier than the current time.
func (s *Service) Register(inc Incident) (*Rat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if inc.ID == "" || inc.LinkID == "" || inc.Start == nil {
		s.log("REGISTER reject: invalid argument")
		return nil, ErrInvalidArgument
	}
	if inc.Start.Cmp(s.eng.now) < 0 {
		s.log("REGISTER reject: clock rewind")
		return nil, ErrClockRewind
	}
	if _, ok := s.eng.net.links[inc.LinkID]; !ok {
		s.log("REGISTER reject: link not found")
		return nil, ErrLinkNotFound
	}
	if _, dup := s.eng.incidents[inc.ID]; dup {
		s.log("REGISTER reject: invalid argument (duplicate id)")
		return nil, ErrInvalidArgument
	}
	if !validRatio(inc.Ratio) {
		s.log("REGISTER reject: ratio out of range")
		return nil, ErrRatioOutOfRange
	}

	s.eng.advance(inc.Start)
	s.eng.incidents[inc.ID] = &incidentState{
		linkID: inc.LinkID,
		start:  ratCopy(inc.Start),
		ratio:  ratCopy(inc.Ratio),
	}
	s.eng.applyExternalChange()
	s.log("REGISTER accept id=" + inc.ID + " link=" + inc.LinkID +
		" start=" + inc.Start.RatString() + " ratio=" + inc.Ratio.RatString() +
		" -> accepted at " + s.eng.now.RatString() +
		" | basis: ratio multiplies capacity; strongest incident on the link wins, queue continues linearly")
	return ratCopy(s.eng.now), nil
}

// validateAt runs the common checks for operations addressed by id, in
// the mandated precedence order.
func (s *Service) validateAt(id string, at *Rat) (*incidentState, error) {
	if id == "" || at == nil {
		return nil, ErrInvalidArgument
	}
	if at.Cmp(s.eng.now) < 0 {
		return nil, ErrClockRewind
	}
	inc, ok := s.eng.incidents[id]
	if !ok {
		return nil, ErrIncidentNotFound
	}
	if inc.ended {
		return nil, ErrIncidentAlreadyEnded
	}
	return inc, nil
}

// Update changes the reduction ratio of an active incident at time at.
// The incident must have started already. Earlier queue evolution is
// untouched; the new ratio applies from at onward.
func (s *Service) Update(id string, at *Rat, ratio *Rat) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inc, err := s.validateAt(id, at)
	if err != nil {
		s.log("UPDATE reject id=" + id + ": " + err.Error())
		return err
	}
	if !validRatio(ratio) {
		s.log("UPDATE reject id=" + id + ": ratio out of range")
		return ErrRatioOutOfRange
	}
	if at.Cmp(inc.start) < 0 {
		s.log("UPDATE reject id=" + id + ": before incident start")
		return ErrInvalidArgument
	}

	s.eng.advance(at)
	inc.ratio = ratCopy(ratio)
	s.eng.applyExternalChange()
	s.log("UPDATE accept id=" + id + " at=" + at.RatString() +
		" ratio=" + ratio.RatString() +
		" -> accepted | basis: earlier queue untouched; new drift applies from this instant")
	return nil
}

// Clear ends an active incident at time at. Clearing a missing or already
// ended incident is rejected without changing any state.
func (s *Service) Clear(id string, at *Rat) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	inc, err := s.validateAt(id, at)
	if err != nil {
		s.log("CLEAR reject id=" + id + ": " + err.Error())
		return err
	}

	s.eng.advance(at)
	inc.ended = true
	s.eng.applyExternalChange()
	s.log("CLEAR accept id=" + id + " at=" + at.RatString() +
		" -> accepted | basis: capacity restored; drains at capacity-arrival, zero lifts upstream limit")
	return nil
}

// Advance moves the simulation time forward; rewinding is rejected.
func (s *Service) Advance(to *Rat) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if to == nil {
		s.log("ADVANCE reject: invalid argument")
		return ErrInvalidArgument
	}
	if to.Cmp(s.eng.now) < 0 {
		s.log("ADVANCE reject: clock rewind")
		return ErrClockRewind
	}
	s.eng.advance(to)
	s.log("ADVANCE accept to=" + to.RatString() + " now=" + s.eng.now.RatString())
	return nil
}

// Query returns queue length (vehicles) and affected level of one link
// at the current simulation time. Cost is O(1) in the number of
// unaffected links: it is a direct map lookup of precomputed state;
// recomputation only ever happens inside event-driven Advance.
func (s *Service) Query(linkID string) (LinkState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if linkID == "" {
		return LinkState{}, ErrInvalidArgument
	}
	if _, ok := s.eng.net.links[linkID]; !ok {
		return LinkState{}, ErrLinkNotFound
	}
	q, ok := s.eng.queue[linkID]
	if !ok {
		q = newRat()
	}
	return LinkState{
		LinkID: linkID,
		Queue:  ratCopy(q),
		Level:  s.eng.level[linkID],
	}, nil
}
