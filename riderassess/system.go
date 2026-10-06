package riderassess

// 本文件是对外门面，统一持有全局锁（并发协调模块），并编排：
// 单调时钟校验、事件登记与簇归并、周期结算与定级、申诉裁决、回溯补偿。

import "sync"

// System 骑手考核与申诉系统。
type System struct {
	mu            sync.Mutex
	cfg           Config
	maxTs         int64
	riders        map[string]struct{}
	events        map[string]*Event
	appeals       map[string]*Appeal
	appealOfEvent map[string]*Appeal
	rstate        map[string]*riderState
}

type riderState struct {
	sets         map[string]*rootSet // 根因标识 -> 未撤销事件集
	totals       map[int]int         // 周期 -> 未撤销计扣分事件的实时扣分总额
	scoreSnap    map[int]int         // 已结算周期的冻结扣分
	gradeSnap    map[int]int         // 已结算周期的冻结（本周期应得）等级
	benefitSnap  map[int]int         // 已结算周期生效的权益等级
	baseOverride map[int]int         // 回溯：周期 p 的下降上限基准覆盖值
	nextFreeze   int                 // 下一个待冻结的周期
	comps        []Compensation
}

// New 构造系统并校验全部参数。
func New(cfg Config) (*System, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return &System{
		cfg:           cfg,
		riders:        map[string]struct{}{},
		events:        map[string]*Event{},
		appeals:       map[string]*Appeal{},
		appealOfEvent: map[string]*Appeal{},
		rstate:        map[string]*riderState{},
	}, nil
}

// RegisterRider 注册骑手（幂等）。
func (s *System) RegisterRider(opTs int64, rider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opTs); err != nil {
		return err
	}
	if rider == "" {
		return errf(ErrInvalidParam, "rider id is empty")
	}
	if _, ok := s.riders[rider]; !ok {
		s.riders[rider] = struct{}{}
		s.rstate[rider] = newRiderState()
	}
	s.maxTs = opTs
	return nil
}

// RegisterEvent 登记一条扣分事件并完成簇归并。
func (s *System) RegisterEvent(opTs int64, ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opTs); err != nil {
		return err
	}
	if ev.ID == "" || ev.Rider == "" {
		return errf(ErrInvalidParam, "event id/rider is empty")
	}
	if _, dup := s.events[ev.ID]; dup {
		return errf(ErrInvalidParam, "duplicate event id %q", ev.ID)
	}
	ded, ok := s.cfg.Deductions[ev.Type]
	if !ok || ded <= 0 {
		return errf(ErrInvalidParam, "unknown or non-positive deduction type %q", ev.Type)
	}
	if _, ok := s.riders[ev.Rider]; !ok {
		return errf(ErrRiderNotFound, "rider %q not registered", ev.Rider)
	}
	rs := s.rstate[ev.Rider]
	stored := &Event{
		ID: ev.ID, Rider: ev.Rider, OccurAt: ev.OccurAt,
		Type: ev.Type, RootCause: ev.RootCause,
	}

	if ev.RootCause == "" {
		// 无根因标识：各自独立计扣分，不进入任何簇结构。
		rs.totals[s.cfg.periodIndex(ev.OccurAt)] += ded
		s.events[ev.ID] = stored
		s.maxTs = opTs
		s.freezeThrough(rs, opTs)
		return nil
	}

	set := rs.sets[ev.RootCause]
	if set == nil {
		set = newRootSet()
		rs.sets[ev.RootCause] = set
	}
	res := set.insertEvent(stored, s.cfg.ClusterSpan)
	for _, k := range res.stoppedScorers {
		old := s.events[k.id]
		rs.totals[s.cfg.periodIndex(old.OccurAt)] -= s.cfg.Deductions[old.Type]
	}
	if res.newScorer {
		rs.totals[s.cfg.periodIndex(ev.OccurAt)] += ded
	}
	s.events[ev.ID] = stored
	s.maxTs = opTs
	s.freezeThrough(rs, opTs)
	return nil
}

// FileAppeal 提出申诉。
func (s *System) FileAppeal(opTs int64, id, eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opTs); err != nil {
		return err
	}
	if id == "" || eventID == "" {
		return errf(ErrInvalidParam, "appeal/event id is empty")
	}
	if _, dup := s.appeals[id]; dup {
		return errf(ErrInvalidParam, "duplicate appeal id %q", id)
	}
	ev, ok := s.events[eventID]
	if !ok {
		return errf(ErrEventNotFound, "event %q not found", eventID)
	}
	if _, ok := s.riders[ev.Rider]; !ok {
		return errf(ErrRiderNotFound, "rider %q not registered", ev.Rider)
	}
	if ev.Revoked {
		return errf(ErrEventRevoked, "event %q already revoked", eventID)
	}
	if s.appealOfEvent[eventID] != nil {
		return errf(ErrAlreadyAppealed, "event %q already appealed", eventID)
	}
	if ev.RootCause != "" {
		set := s.rstate[ev.Rider].sets[ev.RootCause]
		if set != nil && set.isSatellite(eventKey{ev.OccurAt, ev.ID}) {
			return errf(ErrSatelliteEvent, "event %q is a satellite event in its cluster", eventID)
		}
	}
	if opTs >= ev.OccurAt+s.cfg.AppealWindow {
		return errf(ErrAppealWindowExpired, "appeal for event %q past window", eventID)
	}
	a := &Appeal{ID: id, EventID: eventID, Rider: ev.Rider, FiledAt: opTs}
	s.appeals[id] = a
	s.appealOfEvent[eventID] = a
	s.maxTs = opTs
	return nil
}

// RuleAppeal 裁决申诉；upheld 为 true 表示申诉成立。
func (s *System) RuleAppeal(opTs int64, appealID string, upheld bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opTs); err != nil {
		return err
	}
	if appealID == "" {
		return errf(ErrInvalidParam, "appeal id is empty")
	}
	a, ok := s.appeals[appealID]
	if !ok {
		return errf(ErrAppealNotFound, "appeal %q not found", appealID)
	}
	if a.Ruled {
		return errf(ErrAppealRuled, "appeal %q already ruled", appealID)
	}
	a.Ruled = true
	a.Upheld = upheld
	a.RuledAt = opTs
	if !upheld {
		// 驳回不改任何事件、簇、结算状态，仅记录裁决。
		s.maxTs = opTs
		return nil
	}

	ev := s.events[a.EventID]
	rs := s.rstate[a.Rider]
	var removed []*Event
	if ev.RootCause == "" {
		ev.Revoked = true
		removed = []*Event{ev}
	} else {
		// revokeCluster 按簇内最早者优先返回；连带事件一并撤销。
		keys := rs.sets[ev.RootCause].revokeCluster(eventKey{ev.OccurAt, ev.ID})
		for _, k := range keys {
			e := s.events[k.id]
			e.Revoked = true
			removed = append(removed, e)
		}
	}

	// 撤销只改变簇内最早者（计扣分者）所在周期的扣分；连带事件本就不计扣分。
	scorer := removed[0]
	affectedPeriod := s.cfg.periodIndex(scorer.OccurAt)
	rs.totals[affectedPeriod] -= s.cfg.Deductions[scorer.Type]

	s.maxTs = opTs
	s.freezeThrough(rs, opTs)

	// 回溯：受影响周期此前已冻结时，冻结等级与权益不改写，只补偿并改写未来基准。
	if frozenGrade, frozen := rs.gradeSnap[affectedPeriod]; frozen {
		desiredGrade := s.cfg.gradeOf(rs.totals[affectedPeriod])
		if desiredGrade < frozenGrade {
			desiredBenefit := s.liveBenefit(rs, affectedPeriod)
			rs.comps = append(rs.comps, Compensation{
				AppealID:     appealID,
				Rider:        a.Rider,
				Period:       affectedPeriod,
				FrozenGrade:  frozenGrade,
				DesiredGrade: desiredGrade,
				Levels:       frozenGrade - desiredGrade,
				Amount:       (frozenGrade - desiredGrade) * s.cfg.CompPerLevel,
			})
			// 下一个尚未开始的周期：右端点严格大于裁决时刻的第一个周期。
			// 它一定尚未冻结（冻结只可能发生在右端点之后）。
			q := affectedPeriod + 1
			for s.cfg.periodEnd(q) <= opTs {
				q++
			}
			rs.baseOverride[q] = desiredBenefit
		}
	}
	return nil
}

// QueryPeriod 查询某骑手某周期在 opTs 时刻的扣分、等级与权益。
func (s *System) QueryPeriod(opTs int64, rider string, period int) (PeriodReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(opTs); err != nil {
		return PeriodReport{}, err
	}
	rs, ok := s.rstate[rider]
	if !ok {
		return PeriodReport{}, errf(ErrRiderNotFound, "rider %q not registered", rider)
	}
	// 查询是只读观察：逻辑上冻结到 opTs，但不推进写入时钟 maxTs，
	// 否则一次观察会使后续合法写入被误判为时钟回退。
	s.freezeThrough(rs, opTs)

	if s.cfg.periodEnd(period) <= opTs {
		return PeriodReport{
			Period: period, Settled: true, Frozen: true,
			Score:   rs.scoreSnap[period],
			Grade:   rs.gradeSnap[period],
			Benefit: rs.benefitSnap[period],
		}, nil
	}
	score := rs.totals[period]
	return PeriodReport{
		Period: period, Settled: false, Frozen: false,
		Score:   score,
		Grade:   s.cfg.gradeOf(score),
		Benefit: s.liveBenefit(rs, period),
	}, nil
}

// Event 返回事件快照。
func (s *System) Event(id string) (Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev, ok := s.events[id]
	if !ok {
		return Event{}, false
	}
	return *ev, true
}

// Appeal 返回申诉快照。
func (s *System) Appeal(id string) (Appeal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.appeals[id]
	if !ok {
		return Appeal{}, false
	}
	return *a, true
}

// Compensations 返回某骑手的补偿账目（按产生顺序）。
func (s *System) Compensations(rider string) []Compensation {
	s.mu.Lock()
	defer s.mu.Unlock()
	rs, ok := s.rstate[rider]
	if !ok {
		return nil
	}
	out := make([]Compensation, len(rs.comps))
	copy(out, rs.comps)
	return out
}

func newRiderState() *riderState {
	return &riderState{
		sets:         map[string]*rootSet{},
		totals:       map[int]int{},
		scoreSnap:    map[int]int{},
		gradeSnap:    map[int]int{},
		benefitSnap:  map[int]int{},
		baseOverride: map[int]int{},
	}
}

// checkClock 只读校验；必须在任何状态修改之前调用，以保证被拒绝操作不推进时钟。
func (s *System) checkClock(opTs int64) error {
	if opTs < s.maxTs {
		return errf(ErrClockRollback, "operation time %d earlier than accepted max %d", opTs, s.maxTs)
	}
	return nil
}
