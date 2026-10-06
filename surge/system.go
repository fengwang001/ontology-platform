package surge

import "sync"

// System 是区域运力调度与高峰加价系统。
// 所有公开操作在同一把互斥锁下串行化，天然等价于某一串行顺序；
// 被拒绝的操作在校验阶段返回，不触碰任何状态与时钟。
type System struct {
	mu     sync.Mutex
	cfg    Config
	ledger *ledger
	clock  int64
	events map[string][]*TierEvent
}

// New 校验构造参数并创建系统。
func New(cfg Config) (*System, *Error) {
	if e := cfg.validate(); e != nil {
		return nil, e
	}
	return &System{
		cfg:    cfg,
		ledger: newLedger(),
		events: map[string][]*TierEvent{},
	}, nil
}

// AddArea 登记一个区域。
func (s *System) AddArea(id string) *Error {
	if id == "" {
		return errf(KindInvalidParam, "empty area id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ledger.areas[id]; ok {
		return errf(KindInvalidParam, "area already exists: %s", id)
	}
	s.ledger.addArea(id)
	return nil
}

// AddRider 登记一个骑手（初始离线）。
func (s *System) AddRider(id string) *Error {
	if id == "" {
		return errf(KindInvalidParam, "empty rider id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ledger.riders[id]; ok {
		return errf(KindInvalidParam, "rider already exists: %s", id)
	}
	s.ledger.addRider(id)
	return nil
}

// checkClock 在已持锁状态下校验单调时钟。
func (s *System) checkClock(at int64) *Error {
	if at < s.clock {
		return errf(KindClockRollback, "time %d earlier than %d", at, s.clock)
	}
	return nil
}
