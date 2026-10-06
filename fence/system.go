package fence

import "sync"

// Package fence implements merchant delivery-fence rings with dynamic
// shrink control. A merchant belongs to one region with an immutable base
// range of ring-labeled cells; per-region half-open platform events and an
// immediate merchant level set the effective level as their maximum, and the
// effective radius is max(0, base radius - effective level). All operations
// serialize under one mutex; rejected operations mutate nothing.

// Merchant is the public view of a registered merchant.
type Merchant struct {
	ID     string
	Region string
	Level  int
	br     *baseRange
}

// BaseRadius returns the largest ring distance of the registered footprint.
func (m *Merchant) BaseRadius() int { return m.br.baseRadius() }

// RecoveryInfo is the read-only result of a next-recovery query.
// Reachable is true when the cell is reachable now. NoRecovery is true when
// the merchant's own level alone already keeps the cell shrunk forever.
// Otherwise Next is the earliest time at which the cell becomes reachable
// again assuming registered events end as planned (it may equal now only in
// the Reachable case).
type RecoveryInfo struct {
	Reachable  bool
	NoRecovery bool
	Next       int64
	Reason     Reachability
}

// System is the linearizable fence service. A single mutex serializes every
// operation, so any concurrent execution is equivalent to some serial order;
// replaying the same accepted operation sequence reproduces identical state.
type System struct {
	mu        sync.Mutex
	clock     int64
	merchants map[string]*Merchant
	events    map[string]*platformEvent
	orders    map[string]*Order
	engine    *shrinkEngine
}

func NewSystem() *System {
	return &System{
		clock:     -1,
		merchants: make(map[string]*Merchant),
		events:    make(map[string]*platformEvent),
		orders:    make(map[string]*Order),
		engine:    newShrinkEngine(),
	}
}

// checkClock enforces global monotonic time. Rejected calls never touch it.
func (s *System) checkClock(t int64) error {
	if t < 0 {
		return fail(ErrInvalidArgument, "negative timestamp %d", t)
	}
	if t < s.clock {
		return fail(ErrClockRollback, "timestamp %d earlier than %d", t, s.clock)
	}
	return nil
}

// effectiveRadius computes baseRadius - max(platformLevel, merchantLevel),
// floored at zero. O(1) amortized, independent of cell and event counts.
func (s *System) effectiveRadius(m *Merchant, t int64) int {
	platform := s.engine.region(m.Region).platformLevelAt(t)
	level := m.Level
	if platform > level {
		level = platform
	}
	r := m.br.baseRadius() - level
	if r < 0 {
		return 0
	}
	return r
}

// RegisterMerchant registers a merchant that belongs to exactly one region.
// cells maps every footprint cell to its non-negative ring distance.
func (s *System) RegisterMerchant(t int64, id, region string, cells map[Cell]int) error {
	if id == "" || region == "" || len(cells) == 0 {
		return fail(ErrInvalidArgument, "bad merchant registration")
	}
	for _, ring := range cells {
		if ring < 0 {
			return fail(ErrInvalidArgument, "negative ring distance")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	if _, exists := s.merchants[id]; exists {
		return fail(ErrInvalidArgument, "merchant %s already registered", id)
	}
	s.merchants[id] = &Merchant{ID: id, Region: region, br: newBaseRange(cells)}
	s.clock = t
	return nil
}

// SetMerchantLevel sets the immediately-effective, open-ended merchant level.
func (s *System) SetMerchantLevel(t int64, merchantID string, level int) error {
	if merchantID == "" || level < 0 {
		return fail(ErrInvalidArgument, "bad merchant level")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	m, ok := s.merchants[merchantID]
	if !ok {
		return fail(ErrMerchantNotFound, "merchant %s not found", merchantID)
	}
	m.Level = level
	s.clock = t
	return nil
}

// AddPlatformEvent registers a half-open [start,end) region event.
func (s *System) AddPlatformEvent(t int64, id, region string, start, end int64, level int) error {
	if id == "" || region == "" || start < 0 || end <= start || level < 0 {
		return fail(ErrInvalidArgument, "bad platform event")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	if _, exists := s.events[id]; exists {
		return fail(ErrInvalidArgument, "event %s already exists", id)
	}
	ev := &platformEvent{ID: id, Region: region, Start: start, End: end, Level: level, TermAt: -1}
	s.events[id] = ev
	s.engine.region(region).addEvent(ev, t)
	s.clock = t
	return nil
}

// TerminateEvent ends an event early. The boundary must not precede its start;
// terminating at or after the planned end is rejected as already terminated.
func (s *System) TerminateEvent(t int64, eventID string, at int64) error {
	if eventID == "" || at < 0 {
		return fail(ErrInvalidArgument, "bad termination")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	ev, ok := s.events[eventID]
	if !ok {
		return fail(ErrEventNotFound, "event %s not found", eventID)
	}
	if ev.Terminated {
		return fail(ErrEventTerminated, "event %s already terminated", eventID)
	}
	if at < ev.Start || at >= ev.End {
		return fail(ErrInvalidArgument, "termination %d outside live interval", at)
	}
	s.engine.region(ev.Region).terminate(ev, t, at)
	s.clock = t
	return nil
}

// PlaceOrder validates reachability at t and creates a pending order.
func (s *System) PlaceOrder(t int64, orderID, merchantID string, cell Cell) error {
	if orderID == "" || merchantID == "" {
		return fail(ErrInvalidArgument, "bad order")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	m, ok := s.merchants[merchantID]
	if !ok {
		return fail(ErrMerchantNotFound, "merchant %s not found", merchantID)
	}
	if _, dup := s.orders[orderID]; dup {
		return fail(ErrInvalidArgument, "order %s already exists", orderID)
	}
	switch m.br.classify(cell, s.effectiveRadius(m, t)) {
	case OutsideForever:
		return fail(ErrOutsideForever, "cell outside base range forever")
	case ShrunkTemporarily:
		return fail(ErrTemporarilyUnreachable, "cell shrunk at t=%d", t)
	}
	s.orders[orderID] = &Order{ID: orderID, MerchantID: merchantID, Cell: cell, Status: OrderPending, PlacedAt: t}
	s.clock = t
	return nil
}

// AcceptOrder re-validates reachability. When the cell is now shrunk the
// accept is rejected AND the order reaches the cancelled-by-shrink terminal
// state: the cell was reachable at placement, so it can never be outside.
func (s *System) AcceptOrder(t int64, orderID string) error {
	if orderID == "" {
		return fail(ErrInvalidArgument, "bad accept")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return fail(ErrOrderNotFound, "order %s not found", orderID)
	}
	if err := o.canAccept(); err != nil {
		return err
	}
	m := s.merchants[o.MerchantID]
	switch m.br.classify(o.Cell, s.effectiveRadius(m, t)) {
	case OutsideForever:
		panic("fence: invariant violated: cell reachable at placement is outside forever")
	case ShrunkTemporarily:
		o.cancelByShrink()
		s.clock = t
		return fail(ErrTemporarilyUnreachable, "order %s cell shrunk at accept t=%d", orderID, t)
	}
	o.Status = OrderAccepted
	s.clock = t
	return nil
}

// RerouteOrder changes the delivery cell once, using the level at t. A
// rejection leaves the order and its original cell untouched.
func (s *System) RerouteOrder(t int64, orderID string, cell Cell) error {
	if orderID == "" {
		return fail(ErrInvalidArgument, "bad reroute")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return fail(ErrOrderNotFound, "order %s not found", orderID)
	}
	if err := o.canReroute(); err != nil {
		return err
	}
	m := s.merchants[o.MerchantID]
	switch m.br.classify(cell, s.effectiveRadius(m, t)) {
	case OutsideForever:
		return fail(ErrOutsideForever, "new cell outside base range forever")
	case ShrunkTemporarily:
		return fail(ErrTemporarilyUnreachable, "new cell shrunk at t=%d", t)
	}
	o.Cell = cell
	o.Rerouted = true
	s.clock = t
	return nil
}

// DeliverOrder completes an accepted order; later shrink never affects it.
func (s *System) DeliverOrder(t int64, orderID string) error {
	if orderID == "" {
		return fail(ErrInvalidArgument, "bad deliver")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return fail(ErrOrderNotFound, "order %s not found", orderID)
	}
	if err := o.canDeliver(); err != nil {
		return err
	}
	o.Status = OrderDelivered
	s.clock = t
	return nil
}

// QueryReachable is a read-only reachability check. The two unreachable
// reasons are carried by the returned Reachability rather than by an error;
// errors are reserved for invalid arguments, clock rollback and missing
// merchants. It never mutates state or the clock.
func (s *System) QueryReachable(t int64, merchantID string, cell Cell) (Reachability, error) {
	if merchantID == "" {
		return OutsideForever, fail(ErrInvalidArgument, "bad query")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return OutsideForever, err
	}
	m, ok := s.merchants[merchantID]
	if !ok {
		return OutsideForever, fail(ErrMerchantNotFound, "merchant %s not found", merchantID)
	}
	return m.br.classify(cell, s.effectiveRadius(m, t)), nil
}

// QueryNextRecovery answers the three-case recovery query without mutation.
func (s *System) QueryNextRecovery(t int64, merchantID string, cell Cell) (RecoveryInfo, error) {
	if merchantID == "" {
		return RecoveryInfo{Reason: OutsideForever}, fail(ErrInvalidArgument, "bad query")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(t); err != nil {
		return RecoveryInfo{}, err
	}
	m, ok := s.merchants[merchantID]
	if !ok {
		return RecoveryInfo{}, fail(ErrMerchantNotFound, "merchant %s not found", merchantID)
	}
	ring, inRange := m.br.rings[cell]
	if !inRange {
		return RecoveryInfo{Reason: OutsideForever}, fail(ErrOutsideForever, "cell outside base range forever")
	}
	region := s.engine.region(m.Region)
	if m.br.classify(cell, s.effectiveRadius(m, t)) == Reachable {
		return RecoveryInfo{Reachable: true, Next: t, Reason: Reachable}, nil
	}
	view := region.view()
	// The cell is shrunk now. If the merchant level alone blocks it, no
	// registered event ending can ever restore it.
	merchantRadius := m.br.baseRadius() - m.Level
	if merchantRadius < 0 {
		merchantRadius = 0
	}
	if ring > merchantRadius {
		return RecoveryInfo{NoRecovery: true, Reason: ShrunkTemporarily}, nil
	}
	// Walk scheduled live boundaries strictly after t; at each candidate the
	// effective level can only drop, so the first boundary at which the
	// platform max falls to/below the threshold is the earliest recovery.
	cur := t
	for {
		end, ok := view.nextEnd(cur)
		if !ok {
			return RecoveryInfo{NoRecovery: true, Reason: ShrunkTemporarily}, nil
		}
		platform := view.levelAt(end)
		lvl := m.Level
		if platform > lvl {
			lvl = platform
		}
		radius := m.br.baseRadius() - lvl
		if radius < 0 {
			radius = 0
		}
		if ring <= radius {
			return RecoveryInfo{Next: end, Reason: ShrunkTemporarily}, nil
		}
		cur = end
	}
}

// OrderInfo is the public snapshot of an order.
type OrderInfo struct {
	ID           string
	MerchantID   string
	Cell         Cell
	Status       OrderStatus
	CancelReason CancelReason
	Rerouted     bool
	PlacedAt     int64
}

// GetOrder returns an order snapshot for tests and verification.
func (s *System) GetOrder(orderID string) (OrderInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[orderID]
	if !ok {
		return OrderInfo{}, fail(ErrOrderNotFound, "order %s not found", orderID)
	}
	return OrderInfo{o.ID, o.MerchantID, o.Cell, o.Status, o.CancelReason, o.Rerouted, o.PlacedAt}, nil
}
