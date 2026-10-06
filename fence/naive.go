package fence

import "sort"

// NaiveSystem is an independent reference implementation. It deliberately
// avoids every index used by System: the effective level at time t is found
// by linearly scanning every registered event in the region (live events use
// their possibly terminated boundary), and reachability scans the full cell
// map. Each query is therefore O(cells + events); it exists only to cross
// check the indexed implementation on random operation sequences.
type NaiveSystem struct {
	clock     int64
	merchants map[string]*naiveMerchant
	events    map[string]*platformEvent
	orders    map[string]*Order
}

type naiveMerchant struct {
	id     string
	region string
	cells  map[Cell]int
	level  int
}

func (m *naiveMerchant) baseRadius() int {
	r := 0
	for _, ring := range m.cells {
		if ring > r {
			r = ring
		}
	}
	return r
}

func NewNaiveSystem() *NaiveSystem {
	return &NaiveSystem{
		clock:     -1,
		merchants: make(map[string]*naiveMerchant),
		events:    make(map[string]*platformEvent),
		orders:    make(map[string]*Order),
	}
}

func (n *NaiveSystem) checkClock(t int64) error {
	if t < 0 {
		return fail(ErrInvalidArgument, "negative timestamp %d", t)
	}
	if t < n.clock {
		return fail(ErrClockRollback, "timestamp %d earlier than %d", t, n.clock)
	}
	return nil
}

// effectiveLevel scans every event of the region, one by one.
func (n *NaiveSystem) effectiveLevel(m *naiveMerchant, t int64) int {
	level := m.level
	for _, ev := range n.events {
		if ev.Region != m.region {
			continue
		}
		if ev.liveAt(t) && ev.Level > level {
			level = ev.Level
		}
	}
	return level
}

// classify scans every cell of the footprint.
func (n *NaiveSystem) classify(m *naiveMerchant, t int64, cell Cell) Reachability {
	ring, ok := m.cells[cell]
	if !ok {
		return OutsideForever
	}
	eff := m.baseRadius() - n.effectiveLevel(m, t)
	if eff < 0 {
		eff = 0
	}
	if ring <= eff {
		return Reachable
	}
	return ShrunkTemporarily
}

func (n *NaiveSystem) RegisterMerchant(t int64, id, region string, cells map[Cell]int) error {
	if id == "" || region == "" || len(cells) == 0 {
		return fail(ErrInvalidArgument, "bad merchant registration")
	}
	for _, ring := range cells {
		if ring < 0 {
			return fail(ErrInvalidArgument, "negative ring distance")
		}
	}
	if err := n.checkClock(t); err != nil {
		return err
	}
	if _, exists := n.merchants[id]; exists {
		return fail(ErrInvalidArgument, "merchant %s already registered", id)
	}
	cp := make(map[Cell]int, len(cells))
	for c, r := range cells {
		cp[c] = r
	}
	n.merchants[id] = &naiveMerchant{id: id, region: region, cells: cp}
	n.clock = t
	return nil
}

func (n *NaiveSystem) SetMerchantLevel(t int64, merchantID string, level int) error {
	if merchantID == "" || level < 0 {
		return fail(ErrInvalidArgument, "bad merchant level")
	}
	if err := n.checkClock(t); err != nil {
		return err
	}
	m, ok := n.merchants[merchantID]
	if !ok {
		return fail(ErrMerchantNotFound, "merchant %s not found", merchantID)
	}
	m.level = level
	n.clock = t
	return nil
}

func (n *NaiveSystem) AddPlatformEvent(t int64, id, region string, start, end int64, level int) error {
	if id == "" || region == "" || start < 0 || end <= start || level < 0 {
		return fail(ErrInvalidArgument, "bad platform event")
	}
	if err := n.checkClock(t); err != nil {
		return err
	}
	if _, exists := n.events[id]; exists {
		return fail(ErrInvalidArgument, "event %s already exists", id)
	}
	n.events[id] = &platformEvent{ID: id, Region: region, Start: start, End: end, Level: level, TermAt: -1}
	n.clock = t
	return nil
}

func (n *NaiveSystem) TerminateEvent(t int64, eventID string, at int64) error {
	if eventID == "" || at < 0 {
		return fail(ErrInvalidArgument, "bad termination")
	}
	if err := n.checkClock(t); err != nil {
		return err
	}
	ev, ok := n.events[eventID]
	if !ok {
		return fail(ErrEventNotFound, "event %s not found", eventID)
	}
	if ev.Terminated {
		return fail(ErrEventTerminated, "event %s already terminated", eventID)
	}
	if at < ev.Start || at >= ev.End {
		return fail(ErrInvalidArgument, "termination %d outside live interval", at)
	}
	ev.Terminated = true
	ev.TermAt = at
	n.clock = t
	return nil
}

func (n *NaiveSystem) PlaceOrder(t int64, orderID, merchantID string, cell Cell) error {
	if orderID == "" || merchantID == "" {
		return fail(ErrInvalidArgument, "bad order")
	}
	if err := n.checkClock(t); err != nil {
		return err
	}
	m, ok := n.merchants[merchantID]
	if !ok {
		return fail(ErrMerchantNotFound, "merchant %s not found", merchantID)
	}
	if _, dup := n.orders[orderID]; dup {
		return fail(ErrInvalidArgument, "order %s already exists", orderID)
	}
	switch n.classify(m, t, cell) {
	case OutsideForever:
		return fail(ErrOutsideForever, "cell outside base range forever")
	case ShrunkTemporarily:
		return fail(ErrTemporarilyUnreachable, "cell shrunk at t=%d", t)
	}
	n.orders[orderID] = &Order{ID: orderID, MerchantID: merchantID, Cell: cell, Status: OrderPending, PlacedAt: t}
	n.clock = t
	return nil
}

func (n *NaiveSystem) AcceptOrder(t int64, orderID string) error {
	if orderID == "" {
		return fail(ErrInvalidArgument, "bad accept")
	}
	if err := n.checkClock(t); err != nil {
		return err
	}
	o, ok := n.orders[orderID]
	if !ok {
		return fail(ErrOrderNotFound, "order %s not found", orderID)
	}
	if err := o.canAccept(); err != nil {
		return err
	}
	m := n.merchants[o.MerchantID]
	switch n.classify(m, t, o.Cell) {
	case OutsideForever:
		panic("fence: invariant violated: cell reachable at placement is outside forever")
	case ShrunkTemporarily:
		o.cancelByShrink()
		n.clock = t
		return fail(ErrTemporarilyUnreachable, "order %s cell shrunk at accept t=%d", orderID, t)
	}
	o.Status = OrderAccepted
	n.clock = t
	return nil
}

func (n *NaiveSystem) RerouteOrder(t int64, orderID string, cell Cell) error {
	if orderID == "" {
		return fail(ErrInvalidArgument, "bad reroute")
	}
	if err := n.checkClock(t); err != nil {
		return err
	}
	o, ok := n.orders[orderID]
	if !ok {
		return fail(ErrOrderNotFound, "order %s not found", orderID)
	}
	if err := o.canReroute(); err != nil {
		return err
	}
	m := n.merchants[o.MerchantID]
	switch n.classify(m, t, cell) {
	case OutsideForever:
		return fail(ErrOutsideForever, "new cell outside base range forever")
	case ShrunkTemporarily:
		return fail(ErrTemporarilyUnreachable, "new cell shrunk at t=%d", t)
	}
	o.Cell = cell
	o.Rerouted = true
	n.clock = t
	return nil
}

func (n *NaiveSystem) DeliverOrder(t int64, orderID string) error {
	if orderID == "" {
		return fail(ErrInvalidArgument, "bad deliver")
	}
	if err := n.checkClock(t); err != nil {
		return err
	}
	o, ok := n.orders[orderID]
	if !ok {
		return fail(ErrOrderNotFound, "order %s not found", orderID)
	}
	if err := o.canDeliver(); err != nil {
		return err
	}
	o.Status = OrderDelivered
	n.clock = t
	return nil
}

func (n *NaiveSystem) QueryReachable(t int64, merchantID string, cell Cell) (Reachability, error) {
	if merchantID == "" {
		return OutsideForever, fail(ErrInvalidArgument, "bad query")
	}
	if err := n.checkClock(t); err != nil {
		return OutsideForever, err
	}
	m, ok := n.merchants[merchantID]
	if !ok {
		return OutsideForever, fail(ErrMerchantNotFound, "merchant %s not found", merchantID)
	}
	return n.classify(m, t, cell), nil
}

// QueryNextRecovery collects all distinct candidate boundaries from scratch
// and re-scans every event at each candidate.
func (n *NaiveSystem) QueryNextRecovery(t int64, merchantID string, cell Cell) (RecoveryInfo, error) {
	if merchantID == "" {
		return RecoveryInfo{Reason: OutsideForever}, fail(ErrInvalidArgument, "bad query")
	}
	if err := n.checkClock(t); err != nil {
		return RecoveryInfo{}, err
	}
	m, ok := n.merchants[merchantID]
	if !ok {
		return RecoveryInfo{}, fail(ErrMerchantNotFound, "merchant %s not found", merchantID)
	}
	ring, inRange := m.cells[cell]
	if !inRange {
		return RecoveryInfo{Reason: OutsideForever}, fail(ErrOutsideForever, "cell outside base range forever")
	}
	if n.classify(m, t, cell) == Reachable {
		return RecoveryInfo{Reachable: true, Next: t, Reason: Reachable}, nil
	}
	neededLevel := m.baseRadius() - ring
	if m.level > neededLevel {
		return RecoveryInfo{NoRecovery: true, Reason: ShrunkTemporarily}, nil
	}
	candidateSet := map[int64]struct{}{}
	for _, ev := range n.events {
		if ev.Region != m.region {
			continue
		}
		if ev.End > t {
			candidateSet[ev.End] = struct{}{}
		}
		if ev.Terminated && ev.TermAt > t {
			candidateSet[ev.TermAt] = struct{}{}
		}
	}
	candidates := make([]int64, 0, len(candidateSet))
	for c := range candidateSet {
		candidates = append(candidates, c)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })
	for _, c := range candidates {
		lvl := n.effectiveLevel(m, c)
		if ring <= m.baseRadius()-lvl {
			return RecoveryInfo{Next: c, Reason: ShrunkTemporarily}, nil
		}
	}
	return RecoveryInfo{NoRecovery: true, Reason: ShrunkTemporarily}, nil
}

func (n *NaiveSystem) GetOrder(orderID string) (OrderInfo, error) {
	o, ok := n.orders[orderID]
	if !ok {
		return OrderInfo{}, fail(ErrOrderNotFound, "order %s not found", orderID)
	}
	return OrderInfo{o.ID, o.MerchantID, o.Cell, o.Status, o.CancelReason, o.Rerouted, o.PlacedAt}, nil
}
