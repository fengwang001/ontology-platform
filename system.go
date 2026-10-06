package ontology

import "sync"

const daySeconds int64 = 24 * 60 * 60

type Config struct {
	BaseTime             int64
	WeekSeconds          int64
	ClosingLeadSeconds   int64
	MaxClosureSeconds    int64
	MinClosureGapSeconds int64
	MaxAdvanceDays       int64
}

type System struct {
	cfg      Config
	mu       sync.Mutex
	hasClock bool
	lastAt   int64
	active   *scheduleVersion
	pending  *scheduleVersion
	closures closureState
	orders   orderBook
}

func NewSystem(cfg Config) (*System, error) {
	if cfg.WeekSeconds <= 0 || cfg.ClosingLeadSeconds < 0 || cfg.MaxClosureSeconds <= 0 || cfg.MinClosureGapSeconds < 0 || cfg.MaxAdvanceDays < 0 {
		return nil, &CallError{Code: ErrInvalidParameter, Message: "invalid system configuration"}
	}
	return &System{
		cfg:    cfg,
		orders: orderBook{orders: make(map[string]*Order)},
	}, nil
}

func (s *System) SubmitSchedule(at int64, schedule WeeklySchedule) error {
	version, err := newScheduleVersion(schedule, at, s.cfg.BaseTime, s.cfg.WeekSeconds)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	s.promote(at)
	s.pending = version
	return nil
}

func (s *System) AcceptInstant(orderID string, at, preparationSeconds int64) error {
	if orderID == "" || preparationSeconds < 0 {
		return invalidParameter("invalid instant order")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	if _, exists := s.orders.orders[orderID]; exists {
		return invalidParameter("order already exists")
	}
	s.promote(at)
	if s.closures.forced {
		return forcedClosureError()
	}
	if s.closures.activeAt(at) {
		return temporaryClosureError()
	}
	if s.active == nil {
		return notOpenError()
	}
	end, open := s.active.intervalEndContaining(at, s.cfg.BaseTime, s.cfg.WeekSeconds)
	if !open {
		return notOpenError()
	}
	if end-at < preparationSeconds+s.cfg.ClosingLeadSeconds {
		return &CallError{Code: ErrNearClosing, Message: "not enough time before closing"}
	}
	return s.orders.add(&Order{
		ID:               orderID,
		Kind:             OrderInstant,
		AcceptedAt:       at,
		PromisedPickupAt: at + preparationSeconds,
		Preparation:      preparationSeconds,
		Status:           OrderAccepted,
	})
}

func (s *System) AcceptReservation(orderID string, at, pickupAt, preparationSeconds int64) error {
	if orderID == "" || preparationSeconds < 0 || pickupAt < at {
		return invalidParameter("invalid reservation order")
	}
	maxPickup := at + s.cfg.MaxAdvanceDays*daySeconds
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	if _, exists := s.orders.orders[orderID]; exists {
		return invalidParameter("order already exists")
	}
	if pickupAt > maxPickup {
		return &CallError{Code: ErrReservationFar, Message: "reservation pickup is too far in the future"}
	}
	s.promote(at)
	if s.closures.forced {
		return forcedClosureError()
	}
	schedule := s.active
	if s.pending != nil && pickupAt >= s.pending.effectiveAt {
		schedule = s.pending
	}
	if schedule == nil {
		return notOpenError()
	}
	preparationStart := pickupAt - preparationSeconds
	if schedule == s.pending && preparationStart < schedule.effectiveAt {
		return notOpenError()
	}
	end, open := schedule.intervalEndContaining(preparationStart, s.cfg.BaseTime, s.cfg.WeekSeconds)
	if !open || pickupAt > end {
		return notOpenError()
	}
	if s.closures.contains(pickupAt) {
		return temporaryClosureError()
	}
	return s.orders.add(&Order{
		ID:               orderID,
		Kind:             OrderReservation,
		AcceptedAt:       at,
		PromisedPickupAt: pickupAt,
		Preparation:      preparationSeconds,
		Status:           OrderAccepted,
	})
}

func (s *System) StartTemporaryClosure(id string, at, start, durationSeconds int64, cancelOpenInstantOrders bool) error {
	if id == "" || start < at || durationSeconds <= 0 || durationSeconds > s.cfg.MaxClosureSeconds {
		return invalidParameter("invalid temporary closure")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	for _, closure := range s.closures.temporary {
		if closure.ID == id {
			return invalidParameter("temporary closure already exists")
		}
	}
	s.promote(at)
	if s.closures.forced {
		return forcedClosureError()
	}
	closure := &TemporaryClosure{
		ID:           id,
		Start:        start,
		PlannedEnd:   start + durationSeconds,
		CancelOrders: cancelOpenInstantOrders,
	}
	if err := s.closures.add(closure, at, s.cfg.MaxClosureSeconds, s.cfg.MinClosureGapSeconds); err != nil {
		return err
	}
	if s.orders.hasAcceptedInstant() {
		if !cancelOpenInstantOrders {
			s.removeClosure(id)
			return &CallError{Code: ErrOpenOrders, Message: "accepted instant orders have not started"}
		}
		s.orders.cancelAcceptedInstant(at, ResponsibilityMerchant)
	}
	return nil
}

func (s *System) EndTemporaryClosure(id string, at int64) error {
	if id == "" {
		return invalidParameter("temporary closure id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	closure, err := s.findClosure(id)
	if err != nil {
		return err
	}
	if closure.Ended || at < closure.Start || at >= closure.PlannedEnd {
		return &CallError{Code: ErrInvalidState, Message: "temporary closure cannot be ended now"}
	}
	if s.closures.forced {
		return forcedClosureError()
	}
	_, err = s.closures.end(id, at)
	return err
}

func (s *System) ForceClose(at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	if err := s.closures.startForce(at); err != nil {
		return err
	}
	s.orders.cancelAcceptedOrders(at, ResponsibilityPlatform)
	return nil
}

func (s *System) LiftForceClose(at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	return s.closures.liftForce()
}

func (s *System) StartOrder(id string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	return s.orders.start(id, at)
}

func (s *System) CompleteOrder(id string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(at); err != nil {
		return err
	}
	return s.orders.complete(id, at)
}

func (s *System) Order(id string) (Order, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	order, ok := s.orders.orders[id]
	if !ok {
		return Order{}, false
	}
	return *order, true
}

func (s *System) checkClock(at int64) error {
	if s.hasClock && at < s.lastAt {
		return &CallError{Code: ErrClockRollback, Message: "operation timestamp is before an accepted operation"}
	}
	s.hasClock = true
	s.lastAt = at
	return nil
}

func (s *System) promote(at int64) {
	for s.pending != nil && at >= s.pending.effectiveAt {
		s.active = s.pending
		s.pending = nil
	}
}

func (s *System) findClosure(id string) (*TemporaryClosure, error) {
	for _, closure := range s.closures.temporary {
		if closure.ID == id {
			return closure, nil
		}
	}
	return nil, &CallError{Code: ErrNotFound, Message: "temporary closure not found"}
}

func (s *System) removeClosure(id string) {
	for i, closure := range s.closures.temporary {
		if closure.ID == id {
			s.closures.temporary = append(s.closures.temporary[:i], s.closures.temporary[i+1:]...)
			if s.closures.cursor > i {
				s.closures.cursor--
			}
			if i < s.closures.cursor {
				s.closures.cursor = i
			}
			return
		}
	}
	s.closures.cursor = 0
}

func invalidParameter(message string) error {
	return &CallError{Code: ErrInvalidParameter, Message: message}
}

func forcedClosureError() error {
	return &CallError{Code: ErrForcedClosure, Message: "merchant is force-closed"}
}

func temporaryClosureError() error {
	return &CallError{Code: ErrTemporaryClosure, Message: "merchant is temporarily closed"}
}

func notOpenError() error {
	return &CallError{Code: ErrNotOpen, Message: "merchant is not open"}
}
