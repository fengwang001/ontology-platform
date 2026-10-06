package booking

import "sort"

// naiveModel 是独立编写的朴素对照模型：不维护 occupied 计数器，
// 每次名额核算都对全部预约做一次全量扫描；候选时段也全量遍历。
// 逻辑与 Scheduler 各自独立实现，仅共用错误码与参数结构，用于随机
// 操作序列差分测试。
type naiveModel struct {
	cfg     Config
	regions map[string]*naiveRegion
	orders  map[string]*order
	clock   int64
}

type naiveRegion struct {
	name  string
	slots []*slot
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg, regions: map[string]*naiveRegion{}, orders: map[string]*order{}}
}

func (m *naiveModel) tick(now int64) error {
	if now < m.clock {
		return bookingError(ErrClockRollback, "clock moved backwards")
	}
	return nil
}

func (m *naiveModel) findRegionSlot(regionName string, start int64) (*naiveRegion, *slot) {
	r := m.regions[regionName]
	if r == nil {
		return nil, nil
	}
	for _, sl := range r.slots {
		if sl.start == start {
			return r, sl
		}
	}
	return r, nil
}

// countOccupied 全量扫描该区域该时段仍占名额的预约数。
func (m *naiveModel) countOccupied(regionName string, start int64) int {
	n := 0
	for _, o := range m.orders {
		if o.active() && o.region == regionName && o.slotStart == start {
			n++
		}
	}
	return n
}

func (m *naiveModel) addRegion(name string) error {
	if name == "" {
		return bookingError(ErrInvalidParam, "region name empty")
	}
	if _, ok := m.regions[name]; ok {
		return bookingError(ErrInvalidParam, "region duplicated")
	}
	m.regions[name] = &naiveRegion{name: name}
	return nil
}

func (m *naiveModel) addSlot(regionName string, start, end int64, capacity int) error {
	r := m.regions[regionName]
	if r == nil {
		return bookingError(ErrRegionNotFound, "region not found")
	}
	if start >= end || capacity < 0 {
		return bookingError(ErrInvalidParam, "bad slot")
	}
	for _, sl := range r.slots {
		if sl.start == start {
			return bookingError(ErrInvalidParam, "slot duplicated")
		}
		if sl.start < start && sl.end > start || sl.start >= start && sl.start < end {
			return bookingError(ErrInvalidParam, "overlap")
		}
	}
	r.slots = append(r.slots, &slot{start: start, end: end, capacity: capacity})
	sort.Slice(r.slots, func(i, j int) bool { return r.slots[i].start < r.slots[j].start })
	return nil
}

func (m *naiveModel) setCapacity(now int64, regionName string, start int64, capacity int) error {
	if err := m.tick(now); err != nil {
		return err
	}
	if capacity < 0 {
		return bookingError(ErrInvalidParam, "capacity negative")
	}
	if m.regions[regionName] == nil {
		return bookingError(ErrRegionNotFound, "region not found")
	}
	_, sl := m.findRegionSlot(regionName, start)
	if sl == nil {
		return bookingError(ErrSlotNotFound, "slot not found")
	}
	m.clock = now
	sl.capacity = capacity
	return nil
}

func (m *naiveModel) place(now int64, req PlaceRequest) (PlaceResult, error) {
	if req.OrderID == "" || req.Region == "" {
		return PlaceResult{}, bookingError(ErrInvalidParam, "empty")
	}
	if err := m.tick(now); err != nil {
		return PlaceResult{}, err
	}
	if _, dup := m.orders[req.OrderID]; dup {
		return PlaceResult{}, bookingError(ErrInvalidParam, "dup")
	}
	r := m.regions[req.Region]
	if r == nil {
		return PlaceResult{}, bookingError(ErrRegionNotFound, "region not found")
	}
	var target *slot
	for _, sl := range r.slots {
		if sl.start == req.SlotStart {
			target = sl
		}
	}
	if target == nil {
		return PlaceResult{}, bookingError(ErrSlotNotFound, "slot not found")
	}
	if tooEarly, tooLate := leadOK(target.start, now, m.cfg); tooEarly || tooLate {
		if tooEarly {
			return PlaceResult{}, bookingError(ErrTooEarly, "early")
		}
		return PlaceResult{}, bookingError(ErrTooLate, "late")
	}
	orig := req.SlotStart
	postponed := false
	if m.countOccupied(req.Region, target.start) >= target.capacity {
		if !req.AcceptPostpone {
			return PlaceResult{}, bookingError(ErrSlotFull, "full")
		}
		var best *slot
		limit := orig + m.cfg.MaxPostponeSpan
		for _, sl := range r.slots {
			if sl.start <= orig || sl.start > limit {
				continue
			}
			if sl.start-now > m.cfg.MaxBookAhead {
				break
			}
			if m.countOccupied(req.Region, sl.start) < sl.capacity {
				best = sl
				break
			}
		}
		if best == nil {
			return PlaceResult{}, bookingError(ErrSlotFull, "no candidate")
		}
		target, postponed = best, true
	}
	m.orders[req.OrderID] = &order{
		id: req.OrderID, region: req.Region, slotStart: target.start,
		postponed: postponed, origSlot: orig,
	}
	m.clock = now
	return PlaceResult{SlotStart: target.start, Postponed: postponed, OrigSlot: orig}, nil
}

func (m *naiveModel) reschedule(now int64, orderID, regionName string, newStart int64) error {
	if orderID == "" || regionName == "" {
		return bookingError(ErrInvalidParam, "empty")
	}
	if err := m.tick(now); err != nil {
		return err
	}
	o := m.orders[orderID]
	if o == nil {
		return bookingError(ErrOrderNotFound, "order not found")
	}
	r := m.regions[regionName]
	if r == nil {
		return bookingError(ErrRegionNotFound, "region not found")
	}
	var newSlot *slot
	for _, sl := range r.slots {
		if sl.start == newStart {
			newSlot = sl
		}
	}
	if newSlot == nil {
		return bookingError(ErrSlotNotFound, "slot not found")
	}
	switch {
	case o.canceled:
		return bookingError(ErrAlreadyCanceled, "canceled")
	case o.delivered:
		return bookingError(ErrAlreadyDelivered, "delivered")
	case o.isReleasedAt(now, m.cfg):
		return bookingError(ErrAlreadyReleased, "released")
	}
	if o.region == regionName && o.slotStart == newStart {
		return bookingError(ErrNoRescheduleNeeded, "same")
	}
	if now >= o.slotStart-m.cfg.RescheduleLead {
		return bookingError(ErrRescheduleDeadlinePassed, "deadline")
	}
	if tooEarly, tooLate := leadOK(newSlot.start, now, m.cfg); tooEarly || tooLate {
		if tooEarly {
			return bookingError(ErrTooEarly, "early")
		}
		return bookingError(ErrTooLate, "late")
	}
	if m.countOccupied(regionName, newStart) >= newSlot.capacity {
		return bookingError(ErrSlotFull, "full")
	}
	o.region = regionName
	o.slotStart = newStart
	m.clock = now
	return nil
}

func (m *naiveModel) cancel(now int64, orderID string) (bool, error) {
	if orderID == "" {
		return false, bookingError(ErrInvalidParam, "empty")
	}
	if err := m.tick(now); err != nil {
		return false, err
	}
	o := m.orders[orderID]
	if o == nil {
		return false, bookingError(ErrOrderNotFound, "order not found")
	}
	if o.canceled {
		return false, bookingError(ErrAlreadyCanceled, "canceled")
	}
	if o.delivered {
		return false, bookingError(ErrAlreadyDelivered, "delivered")
	}
	after := o.isReleasedAt(now, m.cfg)
	o.canceled = true
	o.cancelAfterRelease = after
	m.clock = now
	return after, nil
}

func (m *naiveModel) deliver(now int64, orderID string) error {
	if orderID == "" {
		return bookingError(ErrInvalidParam, "empty")
	}
	if err := m.tick(now); err != nil {
		return err
	}
	o := m.orders[orderID]
	if o == nil {
		return bookingError(ErrOrderNotFound, "order not found")
	}
	switch {
	case o.canceled:
		return bookingError(ErrAlreadyCanceled, "canceled")
	case o.delivered:
		return bookingError(ErrAlreadyDelivered, "delivered")
	case !o.isReleasedAt(now, m.cfg):
		return bookingError(ErrNotReleased, "not released")
	}
	o.delivered = true
	m.clock = now
	return nil
}

func (m *naiveModel) querySlots(now int64, regionName string, from, to int64) ([]SlotInfo, error) {
	if from > to {
		return nil, bookingError(ErrInvalidParam, "bad range")
	}
	if err := m.tick(now); err != nil {
		return nil, err
	}
	r := m.regions[regionName]
	if r == nil {
		return nil, bookingError(ErrRegionNotFound, "region not found")
	}
	out := make([]SlotInfo, 0)
	m.clock = now
	for _, sl := range r.slots {
		if sl.start >= from && sl.start < to {
			occ := m.countOccupied(regionName, sl.start)
			out = append(out, SlotInfo{
				Start: sl.start, End: sl.end, Capacity: sl.capacity,
				Occupied: occ, Overbooked: occ > sl.capacity,
			})
		}
	}
	return out, nil
}

func (m *naiveModel) queryOrder(now int64, orderID string) (OrderInfo, error) {
	if orderID == "" {
		return OrderInfo{}, bookingError(ErrInvalidParam, "empty")
	}
	if err := m.tick(now); err != nil {
		return OrderInfo{}, err
	}
	o := m.orders[orderID]
	if o == nil {
		return OrderInfo{}, bookingError(ErrOrderNotFound, "order not found")
	}
	m.clock = now
	return o.snapshot(now, m.cfg), nil
}
