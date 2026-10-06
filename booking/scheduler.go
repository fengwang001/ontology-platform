package booking

import "sync"

// Scheduler 是并发安全的预约单排程系统入口。
//
// 并发取舍：用一把互斥锁把全部读/写操作串行化。每个操作在临界区内
// 完成全部校验与名额变更，因此其效果等价于某个串行顺序，改期的
// “释放旧名额 + 占用新名额”天然是一个不可分割的原子步骤，不存在
// 同一预约占两个时段或一个都不占的中间状态。临界区内均为内存计算，
// 无阻塞调用，粗锁足以满足正确性与性能要求。
type Scheduler struct {
	cfg     Config
	mu      sync.Mutex
	regions map[string]*region
	orders  map[string]*order
	clock   int64 // 已接受操作携带的最大时刻
}

// New 构造系统并校验参数。提前量必须非负，且最早不得晚于最晚。
func New(cfg Config) (*Scheduler, error) {
	if cfg.DispatchLead < 0 || cfg.RescheduleLead < 0 ||
		cfg.MinBookAhead < 0 || cfg.MaxBookAhead < 0 || cfg.MaxPostponeSpan < 0 {
		return nil, bookingError(ErrInvalidParam, "lead values must be non-negative")
	}
	if cfg.MinBookAhead > cfg.MaxBookAhead {
		return nil, bookingError(ErrInvalidParam, "MinBookAhead must be <= MaxBookAhead")
	}
	return &Scheduler{
		cfg:     cfg,
		regions: map[string]*region{},
		orders:  map[string]*order{},
	}, nil
}

// tick 仅校验时钟单调性。被拒绝的操作不得推进时钟，因此时钟提交
// （s.clock = now）必须推迟到全部业务校验通过之后。
func (s *Scheduler) tick(now int64) error {
	if now < s.clock {
		return bookingError(ErrClockRollback, "clock moved backwards")
	}
	return nil
}

func (s *Scheduler) AddRegion(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == "" {
		return bookingError(ErrInvalidParam, "region name empty")
	}
	if _, ok := s.regions[name]; ok {
		return bookingError(ErrInvalidParam, "region duplicated")
	}
	s.regions[name] = &region{name: name}
	return nil
}

func (s *Scheduler) AddSlot(regionName string, start, end int64, capacity int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if start >= end {
		return bookingError(ErrInvalidParam, "slot must be half-open [start,end)")
	}
	if capacity < 0 {
		return bookingError(ErrInvalidParam, "capacity negative")
	}
	r, ok := s.regions[regionName]
	if !ok {
		return bookingError(ErrRegionNotFound, "region not found")
	}
	return r.insertSlot(&slot{start: start, end: end, capacity: capacity})
}

// SetCapacity 调整上限：可下调至已占名额之下（进入超额，不驱逐已占者），
// 上调立即生效。已占名额不随容量变化而改变。
func (s *Scheduler) SetCapacity(now int64, regionName string, start int64, capacity int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.tick(now); err != nil {
		return err
	}
	if capacity < 0 {
		return bookingError(ErrInvalidParam, "capacity negative")
	}
	r, ok := s.regions[regionName]
	if !ok {
		return bookingError(ErrRegionNotFound, "region not found")
	}
	sl, ok := r.findSlot(start)
	if !ok {
		return bookingError(ErrSlotNotFound, "slot not found")
	}
	s.clock = now
	sl.capacity = capacity
	return nil
}

// leadOK 校验 [MinBookAhead, MaxBookAhead] 提前量，两端取等允许。
func leadOK(start, now int64, cfg Config) (tooEarly, tooLate bool) {
	d := start - now
	return d < cfg.MinBookAhead, d > cfg.MaxBookAhead
}

// findPostponeCandidate 顺延：取起点严格晚于 orig、跨度不超过
// MaxPostponeSpan（恰等允许）、满足最晚可预约提前量且有余量的最早时段。
// 超额时段 hasRoom()==false，自动不作候选。从 orig 之后第一个时段起
// 线性向后扫描，扫描量只与时段数及其跨度有关，与区域已有预约总数无关。
func findPostponeCandidate(r *region, orig, now int64, cfg Config) *slot {
	limit := orig + cfg.MaxPostponeSpan
	for _, sl := range r.slots {
		if sl.start <= orig {
			continue
		}
		if sl.start > limit {
			return nil
		}
		if sl.start-now > cfg.MaxBookAhead {
			return nil
		}
		if sl.hasRoom() {
			return sl
		}
	}
	return nil
}

// Place 下单。
func (s *Scheduler) Place(now int64, req PlaceRequest) (PlaceResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.OrderID == "" || req.Region == "" {
		return PlaceResult{}, bookingError(ErrInvalidParam, "order id / region empty")
	}
	if err := s.tick(now); err != nil {
		return PlaceResult{}, err
	}
	if _, dup := s.orders[req.OrderID]; dup {
		return PlaceResult{}, bookingError(ErrInvalidParam, "order id duplicated")
	}
	r, ok := s.regions[req.Region]
	if !ok {
		return PlaceResult{}, bookingError(ErrRegionNotFound, "region not found")
	}
	target, ok := r.findSlot(req.SlotStart)
	if !ok {
		return PlaceResult{}, bookingError(ErrSlotNotFound, "slot not found")
	}
	if tooEarly, tooLate := leadOK(target.start, now, s.cfg); tooEarly || tooLate {
		if tooEarly {
			return PlaceResult{}, bookingError(ErrTooEarly, "earlier than MinBookAhead")
		}
		return PlaceResult{}, bookingError(ErrTooLate, "later than MaxBookAhead")
	}

	postponed := false
	origStart := req.SlotStart
	if !target.hasRoom() {
		if !req.AcceptPostpone {
			return PlaceResult{}, bookingError(ErrSlotFull, "slot full and postpone declined")
		}
		cand := findPostponeCandidate(r, req.SlotStart, now, s.cfg)
		if cand == nil {
			return PlaceResult{}, bookingError(ErrSlotFull, "slot full and no postpone candidate")
		}
		target = cand
		postponed = true
	}

	s.clock = now
	target.occupied++
	s.orders[req.OrderID] = &order{
		id:        req.OrderID,
		region:    req.Region,
		slotStart: target.start,
		postponed: postponed,
		origSlot:  origStart,
	}
	return PlaceResult{SlotStart: target.start, Postponed: postponed, OrigSlot: origStart}, nil
}

// Reschedule 改期。成功时旧名额释放与新名额占用在同一临界区内一次完成。
// 改期不触发顺延；新时段无余量则拒绝且原名额保持。
func (s *Scheduler) Reschedule(now int64, orderID, regionName string, newStart int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if orderID == "" || regionName == "" {
		return bookingError(ErrInvalidParam, "order id / region empty")
	}
	if err := s.tick(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return bookingError(ErrOrderNotFound, "order not found")
	}
	r, ok := s.regions[regionName]
	if !ok {
		return bookingError(ErrRegionNotFound, "region not found")
	}
	newSlot, ok := r.findSlot(newStart)
	if !ok {
		return bookingError(ErrSlotNotFound, "slot not found")
	}
	switch {
	case o.canceled:
		return bookingError(ErrAlreadyCanceled, "order canceled")
	case o.delivered:
		return bookingError(ErrAlreadyDelivered, "order delivered")
	case o.isReleasedAt(now, s.cfg):
		return bookingError(ErrAlreadyReleased, "order released")
	}
	if o.region == regionName && o.slotStart == newStart {
		return bookingError(ErrNoRescheduleNeeded, "same slot")
	}
	// 截止线在提前量之前判定：now >= oldStart-RescheduleLead（恰等）即截止。
	if now >= o.slotStart-s.cfg.RescheduleLead {
		return bookingError(ErrRescheduleDeadlinePassed, "past reschedule deadline")
	}
	if tooEarly, tooLate := leadOK(newSlot.start, now, s.cfg); tooEarly || tooLate {
		if tooEarly {
			return bookingError(ErrTooEarly, "earlier than MinBookAhead")
		}
		return bookingError(ErrTooLate, "later than MaxBookAhead")
	}
	if !newSlot.hasRoom() {
		return bookingError(ErrSlotFull, "target slot full")
	}

	oldRegion := s.regions[o.region]
	oldSlot, _ := oldRegion.findSlot(o.slotStart)
	// 原子迁移：先取新名额再放旧名额，或反之均可，关键在于二者同处临界区，
	// 外部无法观察到中间状态。
	s.clock = now
	newSlot.occupied++
	oldSlot.occupied--
	o.region = regionName
	o.slotStart = newStart
	// 落位事实保留：顺延标记与原时段不被改期覆盖。
	return nil
}

// Cancel 取消，释放前后均允许；返回值表示是否为“释放后取消”。
func (s *Scheduler) Cancel(now int64, orderID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if orderID == "" {
		return false, bookingError(ErrInvalidParam, "order id empty")
	}
	if err := s.tick(now); err != nil {
		return false, err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return false, bookingError(ErrOrderNotFound, "order not found")
	}
	if o.canceled {
		return false, bookingError(ErrAlreadyCanceled, "order canceled")
	}
	if o.delivered {
		return false, bookingError(ErrAlreadyDelivered, "order delivered")
	}
	afterRelease := o.isReleasedAt(now, s.cfg)
	sl, _ := s.regions[o.region].findSlot(o.slotStart)
	s.clock = now
	sl.occupied--
	o.canceled = true
	o.cancelAfterRelease = afterRelease
	return afterRelease, nil
}

// Deliver 送达，仅允许在释放后；送达后不再占名额。
func (s *Scheduler) Deliver(now int64, orderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if orderID == "" {
		return bookingError(ErrInvalidParam, "order id empty")
	}
	if err := s.tick(now); err != nil {
		return err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return bookingError(ErrOrderNotFound, "order not found")
	}
	switch {
	case o.canceled:
		return bookingError(ErrAlreadyCanceled, "order canceled")
	case o.delivered:
		return bookingError(ErrAlreadyDelivered, "order delivered")
	case !o.isReleasedAt(now, s.cfg):
		return bookingError(ErrNotReleased, "order not released yet")
	}
	sl, _ := s.regions[o.region].findSlot(o.slotStart)
	s.clock = now
	sl.occupied--
	o.delivered = true
	return nil
}

// QuerySlots 列出区域内起点落在 [from,to) 的时段名额信息。查询不改状态，
// 但仍受单调时钟约束。
func (s *Scheduler) QuerySlots(now int64, regionName string, from, to int64) ([]SlotInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from > to {
		return nil, bookingError(ErrInvalidParam, "from > to")
	}
	if err := s.tick(now); err != nil {
		return nil, err
	}
	r, ok := s.regions[regionName]
	if !ok {
		return nil, bookingError(ErrRegionNotFound, "region not found")
	}
	out := make([]SlotInfo, 0)
	s.clock = now
	for _, sl := range r.slots {
		if sl.start >= from && sl.start < to {
			out = append(out, sl.info())
		}
	}
	return out, nil
}

// QueryOrder 按预约查询当前时段、顺延与释放状态。释放判定为 O(1)。
func (s *Scheduler) QueryOrder(now int64, orderID string) (OrderInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if orderID == "" {
		return OrderInfo{}, bookingError(ErrInvalidParam, "order id empty")
	}
	if err := s.tick(now); err != nil {
		return OrderInfo{}, err
	}
	o, ok := s.orders[orderID]
	if !ok {
		return OrderInfo{}, bookingError(ErrOrderNotFound, "order not found")
	}
	s.clock = now
	return o.snapshot(now, s.cfg), nil
}
