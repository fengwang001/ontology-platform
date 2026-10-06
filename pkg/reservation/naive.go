package reservation

// NaiveSystem is an intentionally simple, per-time reimplementation of the same
// rules. It stores reservation slices and capacity slices and recomputes every
// occupancy sum by scanning all integer times of the affected interval. It
// shares no data structure with System and serves as the differential-testing
// oracle.
type NaiveSystem struct {
	now          int
	feeders      map[int]int
	caps         []CapacityRecord
	holdDuration int
	log          Logger

	rs     []*Reservation
	nextID int64

	// justExpired holds IDs turned void by the most recent settle call.
	justExpired map[int64]bool
}

func NewNaiveSystem(feeders map[int]int, initial []CapacityRecord, cfg Config) *NaiveSystem {
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
	recs := make([]CapacityRecord, len(initial))
	copy(recs, initial)
	sortRecs(recs)
	return &NaiveSystem{
		feeders:      fs,
		caps:         recs,
		holdDuration: cfg.HoldDuration,
		log:          cfg.Log,
		nextID:       1,
	}
}

func sortRecs(a []CapacityRecord) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1].EffectiveAt > a[j].EffectiveAt; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

func (s *NaiveSystem) candidateTimesFrom(start int) []int {
	set := map[int]bool{}
	for _, r := range s.rs {
		if r.End > start {
			if r.Start >= start {
				set[r.Start] = true
			}
			set[r.End] = true
		}
	}
	for _, c := range s.caps {
		if c.EffectiveAt >= start {
			set[c.EffectiveAt] = true
		}
	}
	set[start] = true
	out := make([]int, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sortInts(out)
	return out
}

func (s *NaiveSystem) capAt(tm int) int {
	out := 0
	for _, r := range s.caps {
		if r.EffectiveAt <= tm {
			out = r.Capacity
		}
	}
	return out
}

func (s *NaiveSystem) settle(now int) error {
	if now < s.now {
		return newError(ErrClockRollback, "时钟回退")
	}
	s.justExpired = nil
	for _, r := range s.rs {
		if r.State == StateHolding && now >= r.ExpiresAt {
			r.State = StateVoid
			if s.justExpired == nil {
				s.justExpired = map[int64]bool{}
			}
			s.justExpired[r.ID] = true
		}
	}
	for _, r := range s.rs {
		if r.State == StateConfirmed && r.End <= now {
			r.State = StateCompleted
		}
	}
	s.now = now
	return nil
}

func (s *NaiveSystem) find(id int64) *Reservation {
	for _, r := range s.rs {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// occupying returns reservations that count toward occupancy at t.
func (s *NaiveSystem) occupying(t int) []*Reservation {
	var out []*Reservation
	for _, r := range s.rs {
		if r.Start <= t && t < r.End {
			if r.State == StateHolding || r.State == StateConfirmed {
				out = append(out, r)
			}
		}
	}
	return out
}

func (s *NaiveSystem) naiveCheck(iv Interval, power, feeder, limit int, exclude int64) *CapacityError {
	for t := iv.Start; t < iv.End; t++ {
		sumF, sumG := 0, 0
		for _, r := range s.occupying(t) {
			if r.ID == exclude {
				continue
			}
			sumG += r.Power
			if r.Feeder == feeder {
				sumF += r.Power
			}
		}
		if sumF+power > limit {
			return &CapacityError{Time: t, Level: LevelFeeder}
		}
		if sumG+power > s.capAt(t) {
			return &CapacityError{Time: t, Level: LevelTransformer}
		}
	}
	return nil
}

func (s *NaiveSystem) validateParams(feeder int, iv Interval, power int) (int, error) {
	limit, ok := s.feeders[feeder]
	if !ok {
		return 0, newError(ErrInvalidParam, "馈线不存在")
	}
	if !iv.Valid() || power <= 0 {
		return 0, newError(ErrInvalidParam, "区间非法或功率非正")
	}
	if power > limit {
		return 0, newError(ErrInvalidParam, "功率超过馈线上限")
	}
	return limit, nil
}

func (s *NaiveSystem) Advance(now int) error {
	s.log.Logf("[naive] input Advance(now=%d)", now)
	err := s.settle(now)
	s.log.Logf("[naive] output Advance -> %v", err)
	return err
}

func (s *NaiveSystem) Create(now int, feeder int, iv Interval, power int) (int64, error) {
	s.log.Logf("[naive] input Create(now=%d feeder=%d iv=[%d,%d) power=%d)", now, feeder, iv.Start, iv.End, power)
	limit, err := s.validateParams(feeder, iv, power)
	if err != nil {
		s.log.Logf("[naive] output Create -> %v", err)
		return 0, err
	}
	if err := s.settle(now); err != nil {
		s.log.Logf("[naive] output Create -> %v", err)
		return 0, err
	}
	if cerr := s.naiveCheck(iv, power, feeder, limit, 0); cerr != nil {
		err := newCapacityError(cerr.Time, cerr.Level)
		s.log.Logf("[naive] output Create -> %v", err)
		return 0, err
	}
	id := s.nextID
	s.nextID++
	s.rs = append(s.rs, &Reservation{
		ID: id, Feeder: feeder, Start: iv.Start, End: iv.End, Power: power,
		State: StateHolding, CreatedAt: now, ExpiresAt: now + s.holdDuration,
	})
	s.log.Logf("[naive] output Create -> id=%d", id)
	return id, nil
}

func (s *NaiveSystem) checkOperable(now int, id int64, holding, confirmed bool) (*Reservation, error) {
	s.justExpired = nil
	if err := s.settle(now); err != nil {
		return nil, err
	}
	r := s.find(id)
	if r == nil {
		return nil, newError(ErrNotFound, "预约不存在")
	}
	switch r.State {
	case StateHolding:
		if !holding {
			return nil, newError(ErrStateNotAllowed, "状态不允许")
		}
	case StateConfirmed:
		if !confirmed {
			return nil, newError(ErrStateNotAllowed, "状态不允许")
		}
	default:
		// Void reached via expiry on this call is 占位已到期; other terminal
		// states (voided earlier, cancelled, completed, released) are 状态不允许.
		if r.State == StateVoid && s.justExpired[id] {
			return nil, newError(ErrHoldExpired, "占位已到期")
		}
		if r.State == StateCancelled {
			return nil, newError(ErrCancelled, "已取消")
		}
		return nil, newError(ErrStateNotAllowed, "状态不允许")
	}
	return r, nil
}

func (s *NaiveSystem) Confirm(now int, id int64) error {
	s.log.Logf("[naive] input Confirm(now=%d id=%d)", now, id)
	r, err := s.checkOperable(now, id, true, false)
	if err != nil {
		s.log.Logf("[naive] output Confirm -> %v", err)
		return err
	}
	r.State = StateConfirmed
	if r.End <= now {
		r.State = StateCompleted
	}
	s.log.Logf("[naive] output Confirm -> ok")
	return nil
}

func (s *NaiveSystem) Release(now int, id int64) error {
	s.log.Logf("[naive] input Release(now=%d id=%d)", now, id)
	r, err := s.checkOperable(now, id, true, true)
	if err != nil {
		s.log.Logf("[naive] output Release -> %v", err)
		return err
	}
	r.State = StateReleased
	s.log.Logf("[naive] output Release -> ok")
	return nil
}

func (s *NaiveSystem) Reschedule(now int, id int64, iv Interval, power int) error {
	s.log.Logf("[naive] input Reschedule(now=%d id=%d iv=[%d,%d) power=%d)", now, id, iv.Start, iv.End, power)
	r, err := s.checkOperable(now, id, true, true)
	if err != nil {
		s.log.Logf("[naive] output Reschedule -> %v", err)
		return err
	}
	limit, perr := s.validateParams(r.Feeder, iv, power)
	if perr != nil {
		s.log.Logf("[naive] output Reschedule -> %v", perr)
		return perr
	}
	if r.State == StateConfirmed {
		if r.Start < now {
			if iv.Start != r.Start || power != r.Power || iv.End > r.End || iv.End < now {
				perr = newError(ErrInvalidParam, "已开始的已确认预约只能提前结束")
				s.log.Logf("[naive] output Reschedule -> %v", perr)
				return perr
			}
		} else if iv.Start < now {
			perr = newError(ErrInvalidParam, "起点早于当前")
			s.log.Logf("[naive] output Reschedule -> %v", perr)
			return perr
		}
	}
	if cerr := s.naiveCheck(iv, power, r.Feeder, limit, r.ID); cerr != nil {
		err := newCapacityError(cerr.Time, cerr.Level)
		s.log.Logf("[naive] output Reschedule -> %v", err)
		return err
	}
	r.Start, r.End, r.Power = iv.Start, iv.End, power
	s.log.Logf("[naive] output Reschedule -> ok")
	return nil
}

func (s *NaiveSystem) ChangeCapacity(now int, rec CapacityRecord) ([]int64, error) {
	s.log.Logf("[naive] input ChangeCapacity(now=%d eff=%d cap=%d)", now, rec.EffectiveAt, rec.Capacity)
	if rec.EffectiveAt < now || rec.Capacity <= 0 {
		err := newError(ErrInvalidParam, "容量变更参数非法")
		s.log.Logf("[naive] output ChangeCapacity -> %v", err)
		return nil, err
	}
	if err := s.settle(now); err != nil {
		s.log.Logf("[naive] output ChangeCapacity -> %v", err)
		return nil, err
	}
	old := make([]CapacityRecord, len(s.caps))
	copy(old, s.caps)
	s.caps = append(s.caps, rec)
	sortRecs(s.caps)
	// confirmed-only block
	for _, t := range s.candidateTimesFrom(rec.EffectiveAt) {
		if t >= naiveHorizon(s) {
			break
		}
		sum := 0
		for _, r := range s.rs {
			if r.State == StateConfirmed && r.Start <= t && t < r.End {
				sum += r.Power
			}
		}
		if sum > s.capAt(t) {
			s.caps = old
			err := newCapacityError(t, LevelTransformer)
			s.log.Logf("[naive] output ChangeCapacity -> %v", err)
			return nil, err
		}
	}
	// cancel holds latest-created-first per over time
	var cancelled []int64
	for {
		var victimAt *Reservation
		for _, t := range s.candidateTimesFrom(rec.EffectiveAt) {
			if t >= naiveHorizon(s) {
				break
			}
			sum := 0
			var holds []*Reservation
			for _, r := range s.rs {
				if r.Start <= t && t < r.End {
					if r.State == StateConfirmed {
						sum += r.Power
					} else if r.State == StateHolding {
						sum += r.Power
						holds = append(holds, r)
					}
				}
			}
			if sum > s.capAt(t) {
				var victim *Reservation
				for _, r := range holds {
					if victim == nil || r.CreatedAt > victim.CreatedAt || (r.CreatedAt == victim.CreatedAt && r.ID > victim.ID) {
						victim = r
					}
				}
				victim.State = StateCancelled
				cancelled = append(cancelled, victim.ID)
				victimAt = victim
				break
			}
		}
		if victimAt == nil {
			break
		}
	}
	s.log.Logf("[naive] output ChangeCapacity -> ok cancelled=%v", cancelled)
	return cancelled, nil
}

func (s *NaiveSystem) Get(id int64) (Reservation, bool) {
	r := s.find(id)
	if r == nil {
		return Reservation{}, false
	}
	return *r, true
}

func (s *NaiveSystem) Now() int { return s.now }

// naiveHorizon bounds the scan: no occupancy or capacity change exists at or
// after the maximum reservation end and final capacity effective time.
func naiveHorizon(s *NaiveSystem) int {
	h := 0
	for _, r := range s.rs {
		if r.End > h {
			h = r.End
		}
	}
	if n := len(s.caps); n > 0 && s.caps[n-1].EffectiveAt+1 > h {
		h = s.caps[n-1].EffectiveAt + 1
	}
	return h
}
