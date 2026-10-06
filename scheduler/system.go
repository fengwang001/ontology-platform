package scheduler

import (
	"math"
	"sync"
)

// System 即时配送预约单排程系统。
// 所有公开方法可并发调用：单互斥锁把每次操作整体串行化，
// 效果等价于某个串行顺序；改期的"释放原名额 + 占用新名额"
// 在临界区内一次完成，任何中间状态不可观察。
type System struct {
	mu      sync.Mutex
	cfg     Config
	last    int64 // 已接受操作的最大时刻
	regions map[string]*region
	ress    map[string]*reservation
}

type reservation struct {
	id                   string
	regionID             string
	slot                 *slot // 当前落位时段
	origStart            int64 // 下单时请求的原始时段起点
	shifted              bool
	canceled             bool
	delivered            bool
	canceledAfterRelease bool
}

func NewSystem(cfg Config) (*System, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &System{
		cfg:     cfg,
		last:    math.MinInt64,
		regions: map[string]*region{},
		ress:    map[string]*reservation{},
	}, nil
}

func (s *System) checkClock(now int64) error {
	if now < s.last {
		return newError(CodeClockRollback, "clock rollback")
	}
	return nil
}

// isReleased 释放是时刻的纯函数：now >= 时段起点 - 派单提前量。
// O(1)，与预约总数无关，无需任何后台清扫。
func (s *System) isReleased(r *reservation, now int64) bool {
	return now >= r.slot.start-s.cfg.DispatchLead
}

// AddRegion 新增区域。
func (s *System) AddRegion(now int64, regionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if regionID == "" {
		return newError(CodeInvalidParam, "empty region id")
	}
	if _, ok := s.regions[regionID]; ok {
		return newError(CodeInvalidParam, "duplicate region")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.regions[regionID] = &region{id: regionID}
	s.last = now
	return nil
}

// AddSlot 在区域内新增时段。
func (s *System) AddSlot(now int64, regionID string, start, end int64, cap int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if end <= start || cap < 0 {
		return newError(CodeInvalidParam, "slot requires end > start and cap >= 0")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	reg, ok := s.regions[regionID]
	if !ok {
		return newError(CodeRegionNotFound, "region not found")
	}
	if err := reg.addSlot(start, end, cap); err != nil {
		return err
	}
	s.last = now
	return nil
}

// SetCapacity 调整时段名额上限。调低到低于已占名额是允许的，
// 时段进入超额状态，不驱逐已占者；调高立即生效。
func (s *System) SetCapacity(now int64, regionID string, slotStart int64, cap int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cap < 0 {
		return newError(CodeInvalidParam, "cap must be >= 0")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	reg, ok := s.regions[regionID]
	if !ok {
		return newError(CodeRegionNotFound, "region not found")
	}
	sl := reg.findSlot(slotStart)
	if sl == nil {
		return newError(CodeSlotNotFound, "slot not found")
	}
	sl.cap = cap
	s.last = now
	return nil
}

// Place 下单预约。时段满且用户接受顺延时，按规则在同区域向后顺延落位。
func (s *System) Place(now int64, resID, regionID string, slotStart int64, acceptShift bool) (PlaceResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if resID == "" || regionID == "" {
		return PlaceResult{}, newError(CodeInvalidParam, "empty id")
	}
	if _, dup := s.ress[resID]; dup {
		return PlaceResult{}, newError(CodeInvalidParam, "duplicate reservation id")
	}
	if err := s.checkClock(now); err != nil {
		return PlaceResult{}, err
	}
	reg, ok := s.regions[regionID]
	if !ok {
		return PlaceResult{}, newError(CodeRegionNotFound, "region not found")
	}
	sl := reg.findSlot(slotStart)
	if sl == nil {
		return PlaceResult{}, newError(CodeSlotNotFound, "slot not found")
	}
	if d := sl.start - now; d < s.cfg.EarliestLead {
		return PlaceResult{}, newError(CodeTooEarly, "slot starts too early")
	} else if d > s.cfg.LatestLead {
		return PlaceResult{}, newError(CodeTooLate, "slot starts too late")
	}
	target := sl
	shifted := false
	if !sl.hasRoom() {
		if !acceptShift {
			return PlaceResult{}, newError(CodeSlotFull, "slot is full")
		}
		cand := reg.shiftCandidate(slotStart, s.cfg.MaxShiftSpan, now+s.cfg.LatestLead)
		if cand == nil {
			return PlaceResult{}, newError(CodeSlotFull, "slot is full and no shift candidate")
		}
		target = cand
		shifted = true
	}
	target.occupied++
	s.ress[resID] = &reservation{id: resID, regionID: regionID, slot: target, origStart: slotStart, shifted: shifted}
	s.last = now
	return PlaceResult{RegionID: regionID, SlotStart: target.start, Shifted: shifted, OrigSlotStart: slotStart}, nil
}

// Reschedule 改期到新时段（可跨区域）。名额迁移原子生效，不触发顺延。
func (s *System) Reschedule(now int64, resID, regionID string, slotStart int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if resID == "" || regionID == "" {
		return newError(CodeInvalidParam, "empty id")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	res, ok := s.ress[resID]
	if !ok {
		return newError(CodeReservationNotFound, "reservation not found")
	}
	reg, ok := s.regions[regionID]
	if !ok {
		return newError(CodeRegionNotFound, "region not found")
	}
	ns := reg.findSlot(slotStart)
	if ns == nil {
		return newError(CodeSlotNotFound, "slot not found")
	}
	if res.canceled {
		return newError(CodeAlreadyCanceled, "reservation canceled")
	}
	if res.delivered {
		return newError(CodeAlreadyDelivered, "reservation delivered")
	}
	if s.isReleased(res, now) {
		return newError(CodeAlreadyReleased, "reservation already released")
	}
	if ns == res.slot {
		return newError(CodeNoChange, "already in target slot")
	}
	if now >= res.slot.start-s.cfg.CutoffLead {
		return newError(CodePastCutoff, "past reschedule cutoff")
	}
	if d := ns.start - now; d < s.cfg.EarliestLead {
		return newError(CodeTooEarly, "slot starts too early")
	} else if d > s.cfg.LatestLead {
		return newError(CodeTooLate, "slot starts too late")
	}
	if !ns.hasRoom() {
		return newError(CodeSlotFull, "slot is full")
	}
	res.slot.occupied--
	ns.occupied++
	res.slot = ns
	res.regionID = regionID
	s.last = now
	return nil
}

// Cancel 取消预约，释放前后都允许；释放后取消会记录标志。
func (s *System) Cancel(now int64, resID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if resID == "" {
		return newError(CodeInvalidParam, "empty id")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	res, ok := s.ress[resID]
	if !ok {
		return newError(CodeReservationNotFound, "reservation not found")
	}
	if res.canceled {
		return newError(CodeAlreadyCanceled, "reservation canceled")
	}
	if res.delivered {
		return newError(CodeAlreadyDelivered, "reservation delivered")
	}
	res.canceled = true
	res.canceledAfterRelease = s.isReleased(res, now)
	res.slot.occupied--
	s.last = now
	return nil
}

// Deliver 送达，只允许在释放后。
func (s *System) Deliver(now int64, resID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if resID == "" {
		return newError(CodeInvalidParam, "empty id")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	res, ok := s.ress[resID]
	if !ok {
		return newError(CodeReservationNotFound, "reservation not found")
	}
	if res.canceled {
		return newError(CodeAlreadyCanceled, "reservation canceled")
	}
	if res.delivered {
		return newError(CodeAlreadyDelivered, "reservation delivered")
	}
	if !s.isReleased(res, now) {
		return newError(CodeNotReleased, "reservation not released yet")
	}
	res.delivered = true
	res.slot.occupied--
	s.last = now
	return nil
}

// GetReservation 按查询时刻返回预约视图。查询不改任何状态（含时钟）。
func (s *System) GetReservation(now int64, resID string) (ReservationView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if resID == "" {
		return ReservationView{}, newError(CodeInvalidParam, "empty id")
	}
	if err := s.checkClock(now); err != nil {
		return ReservationView{}, err
	}
	res, ok := s.ress[resID]
	if !ok {
		return ReservationView{}, newError(CodeReservationNotFound, "reservation not found")
	}
	return ReservationView{
		ID:                   res.id,
		RegionID:             res.regionID,
		SlotStart:            res.slot.start,
		Shifted:              res.shifted,
		OrigSlotStart:        res.origStart,
		Released:             s.isReleased(res, now),
		Canceled:             res.canceled,
		CanceledAfterRelease: res.canceledAfterRelease,
		Delivered:            res.delivered,
	}, nil
}

// ListSlots 按区域与时刻范围 [from, to) 列出相交时段的名额视图。
func (s *System) ListSlots(now int64, regionID string, from, to int64) ([]SlotView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if from > to {
		return nil, newError(CodeInvalidParam, "from must be <= to")
	}
	if err := s.checkClock(now); err != nil {
		return nil, err
	}
	reg, ok := s.regions[regionID]
	if !ok {
		return nil, newError(CodeRegionNotFound, "region not found")
	}
	return reg.listSlots(from, to), nil
}
