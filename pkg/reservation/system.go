package reservation

import "sync"

// Package reservation implements a transformer-capacity reservation system
// with two-level (transformer + feeder) admission checks, hold/confirm flow,
// rescheduling and asymmetric capacity-decrease handling.
//
// Concurrency: a single mutex serializes every public method, so each call's
// effect equals that of some serial execution. The clock is advanced through
// settle (see Advance) which also finalizes expired holds and completed
// confirmed reservations.
type System struct {
	mu sync.Mutex

	now          int
	feeders      map[int]int
	cap          *capacityTable
	holdDuration int
	log          Logger

	store   map[int64]*Reservation
	nextID  int64
	occ     *occupancyIndex
	expires *expiryTreap
}

func NewSystem(feeders map[int]int, initial []CapacityRecord, cfg Config) *System {
	if cfg.HoldDuration <= 0 {
		cfg.HoldDuration = 1
	}
	if cfg.Log == nil {
		cfg.Log = nopLogger{}
	}
	fs := make(map[int]int, len(feeders))
	for id, limit := range feeders {
		fs[id] = limit
	}
	return &System{
		feeders:      fs,
		cap:          newCapacityTable(initial),
		holdDuration: cfg.HoldDuration,
		log:          cfg.Log,
		store:        map[int64]*Reservation{},
		nextID:       1,
		occ:          newOccupancyIndex(),
		expires:      newExpiryTreap(),
	}
}

const maxTime = 1<<31 - 1

// settle advances the settled clock to now: holds with ExpiresAt <= now become
// void and confirmed reservations with End <= now become completed. The expiry
// instant itself counts as expired.
func (s *System) settle(now int) error {
	if now < s.now {
		return newError(ErrClockRollback, "当前时刻只能前进")
	}
	if now == s.now {
		return nil
	}
	s.expires.drain(now, func(expiresAt int, id int64) {
		r := s.store[id]
		if r == nil || r.State != StateHolding || r.ExpiresAt != expiresAt {
			return
		}
		s.occ.remove(r)
		r.State = StateVoid
		s.log.Logf("settle: 预约 %d 占位到期(到期时刻 %d <= 当前 %d) -> 已失效", id, expiresAt, now)
	})
	for _, r := range s.store {
		if r.State == StateConfirmed && r.End <= now {
			s.occ.remove(r)
			r.State = StateCompleted
			s.log.Logf("settle: 预约 %d 终点 %d <= 当前 %d -> 已完成", r.ID, r.End, now)
		}
	}
	s.now = now
	return nil
}

func (s *System) Advance(now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.Logf("input Advance(now=%d)", now)
	if err := s.settle(now); err != nil {
		s.log.Logf("output Advance -> %v", err)
		return err
	}
	s.log.Logf("output Advance -> ok(now=%d)", now)
	return nil
}

func (s *System) validateParams(feeder int, iv Interval, power int) (int, error) {
	limit, ok := s.feeders[feeder]
	if !ok {
		return 0, newError(ErrInvalidParam, "馈线不存在")
	}
	if !iv.Valid() {
		return 0, newError(ErrInvalidParam, "区间非法: 需要 start < end")
	}
	if power <= 0 {
		return 0, newError(ErrInvalidParam, "功率非正")
	}
	if power > limit {
		return 0, newError(ErrInvalidParam, "功率超过馈线上限")
	}
	return limit, nil
}

func (s *System) Create(now int, feeder int, iv Interval, power int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.Logf("input Create(now=%d feeder=%d iv=[%d,%d) power=%d)", now, feeder, iv.Start, iv.End, power)
	limit, err := s.validateParams(feeder, iv, power)
	if err != nil {
		s.log.Logf("output Create -> %v", err)
		return 0, err
	}
	if err := s.settle(now); err != nil {
		s.log.Logf("output Create -> %v", err)
		return 0, err
	}
	if cerr := s.occ.check(iv, power, feeder, limit, nil, s.cap); cerr != nil {
		err := newCapacityError(cerr.Time, cerr.Level)
		s.log.Logf("output Create -> %v (判定: 首个超限时刻 %d 层级 %s)", err, cerr.Time, cerr.Level)
		return 0, err
	}
	id := s.nextID
	s.nextID++
	r := &Reservation{
		ID:        id,
		Feeder:    feeder,
		Start:     iv.Start,
		End:       iv.End,
		Power:     power,
		State:     StateHolding,
		CreatedAt: now,
		ExpiresAt: now + s.holdDuration,
	}
	s.store[id] = r
	s.occ.add(r)
	s.expires.add(r.ExpiresAt, r.ID)
	s.log.Logf("output Create -> id=%d 占位中 到期时刻=%d (两级校验通过)", id, r.ExpiresAt)
	return id, nil
}

func (s *System) Get(id int64) (Reservation, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.store[id]
	if !ok {
		return Reservation{}, false
	}
	return *r, true
}

func (s *System) Now() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// checkOperable enforces the shared rejection order after parameter checks:
// clock rollback > not found > state not allowed > hold expired > cancelled.
func (s *System) checkOperable(now int, id int64, allowHolding, allowConfirmed bool) (*Reservation, error) {
	r := s.store[id]
	if now < s.now {
		return nil, newError(ErrClockRollback, "当前时刻只能前进")
	}
	if r == nil {
		if err := s.settle(now); err != nil {
			return nil, err
		}
		return nil, newError(ErrNotFound, "预约不存在")
	}
	wasHolding := r.State == StateHolding
	if err := s.settle(now); err != nil {
		return nil, err
	}
	if wasHolding && r.State == StateVoid {
		// The only transition holding -> void performed by settle is expiry.
		return nil, newError(ErrHoldExpired, "占位已到期")
	}
	if r.State == StateCancelled {
		return nil, newError(ErrCancelled, "已取消")
	}
	switch r.State {
	case StateHolding:
		if !allowHolding {
			return nil, newError(ErrStateNotAllowed, "占位中的预约不允许该操作")
		}
	case StateConfirmed:
		if !allowConfirmed {
			return nil, newError(ErrStateNotAllowed, "已确认的预约不允许该操作")
		}
	default:
		return nil, newError(ErrStateNotAllowed, "预约处于终态: "+r.State.String())
	}
	return r, nil
}

func (s *System) Confirm(now int, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.Logf("input Confirm(now=%d id=%d)", now, id)
	r, err := s.checkOperable(now, id, true, false)
	if err != nil {
		s.log.Logf("output Confirm(%d) -> %v", id, err)
		return err
	}
	s.occ.remove(r)
	r.State = StateConfirmed
	s.expires.remove(r.ExpiresAt, r.ID)
	if r.End <= now {
		r.State = StateCompleted
		s.log.Logf("output Confirm(%d) -> ok 已确认(终点 %d <= now %d, 立即落定为已完成, 不占用)", id, r.End, now)
		return nil
	}
	s.occ.add(r)
	s.log.Logf("output Confirm(%d) -> ok 已确认 (确认不再校验)", id)
	return nil
}

func (s *System) Release(now int, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.Logf("input Release(now=%d id=%d)", now, id)
	r, err := s.checkOperable(now, id, true, true)
	if err != nil {
		s.log.Logf("output Release(%d) -> %v", id, err)
		return err
	}
	s.occ.remove(r)
	if r.State == StateHolding {
		s.expires.remove(r.ExpiresAt, r.ID)
	}
	r.State = StateReleased
	s.log.Logf("output Release(%d) -> ok 占用立即归还", id)
	return nil
}

func (s *System) Reschedule(now int, id int64, iv Interval, power int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.Logf("input Reschedule(now=%d id=%d iv=[%d,%d) power=%d)", now, id, iv.Start, iv.End, power)
	r, err := s.checkOperable(now, id, true, true)
	if err != nil {
		s.log.Logf("output Reschedule(%d) -> %v", id, err)
		return err
	}
	limit, perr := s.validateParams(r.Feeder, iv, power)
	if perr != nil {
		s.log.Logf("output Reschedule(%d) -> %v", id, perr)
		return perr
	}
	if r.State == StateConfirmed {
		if r.Start < now {
			// Already started: only shortening in place is allowed.
			if iv.Start != r.Start || power != r.Power || iv.End > r.End || iv.End < now {
				perr = newError(ErrInvalidParam, "已开始的已确认预约只能提前结束")
				s.log.Logf("output Reschedule(%d) -> %v", id, perr)
				return perr
			}
		} else if iv.Start < now {
			perr = newError(ErrInvalidParam, "区间起点不得早于当前时刻")
			s.log.Logf("output Reschedule(%d) -> %v", id, perr)
			return perr
		}
	}
	if cerr := s.occ.check(iv, power, r.Feeder, limit, r, s.cap); cerr != nil {
		err := newCapacityError(cerr.Time, cerr.Level)
		s.log.Logf("output Reschedule(%d) -> %v (判定: 排除自身后首个超限时刻 %d 层级 %s, 原预约保留)", id, err, cerr.Time, cerr.Level)
		return err
	}
	s.occ.remove(r)
	r.Start, r.End, r.Power = iv.Start, iv.End, power
	s.occ.add(r)
	s.log.Logf("output Reschedule(%d) -> ok 原子替换 [%d,%d) power=%d 状态=%s 到期时刻=%d 不变", id, iv.Start, iv.End, power, r.State, r.ExpiresAt)
	return nil
}

func (s *System) ChangeCapacity(now int, rec CapacityRecord) ([]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log.Logf("input ChangeCapacity(now=%d eff=%d cap=%d)", now, rec.EffectiveAt, rec.Capacity)
	if rec.EffectiveAt < now {
		err := newError(ErrInvalidParam, "生效时刻早于当前")
		s.log.Logf("output ChangeCapacity -> %v", err)
		return nil, err
	}
	if rec.Capacity <= 0 {
		err := newError(ErrInvalidParam, "容量须为正")
		s.log.Logf("output ChangeCapacity -> %v", err)
		return nil, err
	}
	if err := s.settle(now); err != nil {
		s.log.Logf("output ChangeCapacity -> %v", err)
		return nil, err
	}
	old := s.cap.records()
	s.cap.add(rec)
	if bad := s.occ.checkConfirmedRange(Interval{Start: rec.EffectiveAt, End: maxTime}, s.cap); bad >= 0 {
		s.cap = newCapacityTable(old)
		err := newCapacityError(bad, LevelTransformer)
		s.log.Logf("output ChangeCapacity -> %v (判定: 已确认预约挡住下调, 容量表回滚, 占位不计入)", err)
		return nil, err
	}
	var cancelled []int64
	for {
		bad := s.firstGlobalOverAfter(rec.EffectiveAt)
		if bad < 0 {
			break
		}
		victim := s.latestCreatedHoldAt(bad)
		if victim == nil {
			break
		}
		s.occ.remove(victim)
		s.expires.remove(victim.ExpiresAt, victim.ID)
		victim.State = StateCancelled
		cancelled = append(cancelled, victim.ID)
		s.log.Logf("ChangeCapacity: 时刻 %d 超新容量, 取消占位 %d(createdAt=%d, 同刻创建则 id 大者视为更晚)", bad, victim.ID, victim.CreatedAt)
	}
	s.log.Logf("output ChangeCapacity -> ok eff=%d cap=%d 被取消占位=%v", rec.EffectiveAt, rec.Capacity, cancelled)
	return cancelled, nil
}

// firstGlobalOverAfter returns the earliest t >= start where holding + confirmed
// occupancy exceeds capacity. Candidate times are exactly the global event
// times and capacity effective times at or after start, so irrelevant
// reservations and earlier capacity records are never scanned.
func (s *System) firstGlobalOverAfter(start int) int {
	var times []int
	s.occ.global.keysIn(start, maxTime, &times)
	times = s.cap.effectiveTimesIn(start, maxTime, times)
	sortInts(times)
	if len(times) == 0 || times[0] != start {
		times = append([]int{start}, times...)
	}
	for _, t := range uniqueSorted(times) {
		if s.occ.global.prefixBefore(t+1) > s.cap.at(t) {
			return t
		}
	}
	return -1
}

// latestCreatedHoldAt picks the holding reservation occupying tm with the
// greatest CreatedAt; ties are broken by the greater ID so that replay is
// deterministic (IDs are assigned monotonically).
func (s *System) latestCreatedHoldAt(tm int) *Reservation {
	var best *Reservation
	for _, r := range s.store {
		if r.State != StateHolding || !r.Interval().Contains(tm) {
			continue
		}
		if best == nil || r.CreatedAt > best.CreatedAt || (r.CreatedAt == best.CreatedAt && r.ID > best.ID) {
			best = r
		}
	}
	return best
}
