package rider

import "sync"

// System 为骑手考核与申诉系统。
type System struct {
	cfg          Config
	mu           sync.Mutex
	clock        int64
	riders       map[string]*riderState
	events       map[int64]*Event
	appeals      map[int64]*appealState
	comps        []Compensation
	nextEventID  int64
	nextAppealID int64
}

// New 构造系统并校验参数。
func New(cfg Config) (*System, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	s := &System{
		cfg:          cfg,
		riders:       map[string]*riderState{},
		events:       map[int64]*Event{},
		appeals:      map[int64]*appealState{},
		nextEventID:  1,
		nextAppealID: 1,
	}
	return s, nil
}

func validateConfig(cfg Config) error {
	switch {
	case cfg.PeriodLength <= 0:
		return newError(ErrInvalidConfig, "period length must be positive")
	case cfg.AppealWindow <= 0:
		return newError(ErrInvalidConfig, "appeal window must be positive")
	case cfg.ClusterSpan < 0:
		return newError(ErrInvalidConfig, "cluster span must be non-negative")
	case cfg.MaxLevelDrop <= 0:
		return newError(ErrInvalidConfig, "max level drop must be positive")
	case cfg.CompensationPerLevel < 0:
		return newError(ErrInvalidConfig, "compensation per level must be non-negative")
	}
	want := map[EventType]bool{
		EventTypeLateDelivery:      true,
		EventTypeCustomerComplaint: true,
		EventTypeRejectOrder:       true,
		EventTypeFaultCancel:       true,
	}
	if len(cfg.EventScores) != len(want) {
		return newError(ErrInvalidConfig, "event scores must cover all event types")
	}
	for typ := range want {
		score, ok := cfg.EventScores[typ]
		if !ok {
			return newError(ErrInvalidConfig, "missing event score for "+string(typ))
		}
		if score < 0 {
			return newError(ErrInvalidConfig, "event score must be non-negative")
		}
	}
	for i, v := range cfg.Thresholds {
		if v < 0 {
			return newError(ErrInvalidConfig, "threshold must be non-negative")
		}
		if i > 0 && v <= cfg.Thresholds[i-1] {
			return newError(ErrInvalidConfig, "thresholds must be strictly increasing")
		}
	}
	return nil
}

// tick 校验时钟单调性。调用方持锁。
func (s *System) tick(now int64) error {
	if now < s.clock {
		return newError(ErrClockSkew, "clock moved backwards")
	}
	return nil
}

func (s *System) advance(now int64) {
	if now > s.clock {
		s.clock = now
	}
}

// RegisterRider 登记骑手。
func (s *System) RegisterRider(now int64, riderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if riderID == "" {
		return newError(ErrInvalidArgument, "rider id required")
	}
	if err := s.tick(now); err != nil {
		return err
	}
	if _, ok := s.riders[riderID]; ok {
		return newError(ErrInvalidArgument, "rider already registered")
	}
	s.riders[riderID] = newRiderState(riderID)
	s.advance(now)
	return nil
}

func (s *System) RegisterEvent(now int64, riderID string, t int64, typ EventType, rootKey string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registerEventLocked(now, riderID, t, typ, rootKey)
}

// RegisterEventAtTime 等价于 RegisterEvent；RegisterEventAtomicClock 在锁内分配
// 接受时刻，供并发提交使用，保证接受时刻与串行化顺序一致。
func (s *System) RegisterEventAtomicClock(riderID string, t int64, typ EventType, rootKey string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registerEventLocked(s.clock, riderID, t, typ, rootKey)
}

func (s *System) registerEventLocked(now int64, riderID string, t int64, typ EventType, rootKey string) (int64, error) {
	if riderID == "" {
		return 0, newError(ErrInvalidArgument, "rider id required")
	}
	if _, ok := s.cfg.EventScores[typ]; !ok {
		return 0, newError(ErrInvalidArgument, "unknown event type")
	}
	if err := s.tick(now); err != nil {
		return 0, err
	}
	rs, ok := s.riders[riderID]
	if !ok {
		return 0, newError(ErrRiderNotFound, "rider not found: "+riderID)
	}
	s.settle(rs, now)
	id := s.nextEventID
	s.nextEventID++
	e := &Event{
		ID:      id,
		RiderID: riderID,
		Time:    t,
		Type:    typ,
		RootKey: rootKey,
		Seq:     id,
	}
	s.events[id] = e
	rs.clusters.insert(s, rs, e)
	s.advance(now)
	return id, nil
}

func (s *System) SubmitAppeal(now int64, riderID string, eventID int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if riderID == "" {
		return 0, newError(ErrInvalidArgument, "rider id required")
	}
	if err := s.tick(now); err != nil {
		return 0, err
	}
	rs, ok := s.riders[riderID]
	if !ok {
		return 0, newError(ErrRiderNotFound, "rider not found: "+riderID)
	}
	e, ok := s.events[eventID]
	if !ok {
		return 0, newError(ErrEventNotFound, "event not found")
	}
	if e.RiderID != riderID {
		return 0, newError(ErrEventNotFound, "event not found for rider")
	}
	s.settle(rs, now)
	return s.submitAppeal(rs, now, riderID, eventID)
}

func (s *System) RuleAppeal(now int64, riderID string, appealID int64, upheld bool) (*Compensation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if riderID == "" {
		return nil, newError(ErrInvalidArgument, "rider id required")
	}
	if err := s.tick(now); err != nil {
		return nil, err
	}
	rs, ok := s.riders[riderID]
	if !ok {
		return nil, newError(ErrRiderNotFound, "rider not found: "+riderID)
	}
	a, ok := s.appeals[appealID]
	if !ok {
		return nil, newError(ErrAppealNotFound, "appeal not found")
	}
	if a.riderID != riderID {
		return nil, newError(ErrAppealNotFound, "appeal not found for rider")
	}
	s.settle(rs, now)
	return s.ruleAppeal(rs, now, riderID, appealID, upheld)
}

func (s *System) Query(now int64, riderID string, period int) (QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.tick(now); err != nil {
		return QueryResult{}, err
	}
	rs, ok := s.riders[riderID]
	if !ok {
		return QueryResult{}, newError(ErrRiderNotFound, "rider not found: "+riderID)
	}
	s.settle(rs, now)
	ps := rs.period(period)
	out := QueryResult{Period: period, Settled: ps.settled, Score: ps.liveScore}
	if ps.settled {
		out.Score = ps.frozenScore
		out.Level = ps.level
		out.RightsLevel = ps.rights
	}
	s.advance(now)
	return out, nil
}

// CompensationLog 返回补偿账目的只读快照。
func (s *System) CompensationLog() []Compensation {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Compensation, len(s.comps))
	copy(out, s.comps)
	return out
}
