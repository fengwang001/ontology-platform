package dr

import (
	"fmt"
	"math"
)

func (e *Event) windowEnd() int64 {
	return e.Params.WindowStart + int64(e.Params.WindowIntervals)*e.cfg.IntervalTicks
}

func (e *Event) adjustStart() int64 {
	return e.Params.WindowStart - int64(e.cfg.AdjustmentIntervals)*e.cfg.IntervalTicks
}

// effectiveWindowEnd 返回考核实际使用的窗口终点：
// 窗口开始后取消时截断到取消时刻所在间隔的起点。
func (e *Event) effectiveWindowEnd() int64 {
	end := e.windowEnd()
	if e.Cancelled && e.CancelAfterStart {
		t := e.CancelTick - e.CancelTick%e.cfg.IntervalTicks
		if t < e.Params.WindowStart {
			t = e.Params.WindowStart
		}
		if t < end {
			end = t
		}
	}
	return end
}

// state 由存储标志与当前时刻推导事件状态，只能沿状态机推进。
func (e *Event) state(now int64) State {
	if e.Settled {
		return StateSettled
	}
	if e.Cancelled {
		return StateCancelled
	}
	if now < e.Params.WindowStart {
		return StatePublished
	}
	if now < e.windowEnd() {
		return StateRunning
	}
	return StateEnded
}

func (s *System) validateEventParams(p *EventParams) *Error {
	c := &s.cfg
	if p.Day < 0 || p.WindowStart < 0 || p.WindowIntervals <= 0 {
		return newErr(ErrKindParam, "事件日、窗口起点须非负且窗口长度须为正")
	}
	if p.WindowStart%c.IntervalTicks != 0 {
		return newErr(ErrKindParam, "窗口起点 %d 未对齐计量间隔 %d", p.WindowStart, c.IntervalTicks)
	}
	dayStart := p.Day * c.TicksPerDay
	if p.adjustStartOf(c) < dayStart {
		return newErr(ErrKindParam, "调整期超出事件日边界")
	}
	if p.WindowStart+int64(p.WindowIntervals)*c.IntervalTicks > dayStart+c.TicksPerDay {
		return newErr(ErrKindParam, "事件窗口跨日")
	}
	if !(p.ResponseDeadline < p.ExitDeadline) {
		return newErr(ErrKindParam, "应答截止须早于免责退出截止")
	}
	if p.ExitDeadline > p.WindowStart {
		return newErr(ErrKindParam, "免责退出截止须不晚于窗口起点")
	}
	if p.PayUnitPrice < 0 || p.PenaltyUnitPrice < 0 ||
		math.IsNaN(p.PayUnitPrice) || math.IsNaN(p.PenaltyUnitPrice) {
		return newErr(ErrKindParam, "单价须非负")
	}
	if p.QualifiedRatio <= 0 || p.QualifiedRatio > 1 || math.IsNaN(p.QualifiedRatio) {
		return newErr(ErrKindParam, "履约合格比例须在 (0,1] 内")
	}
	return nil
}

func (p *EventParams) adjustStartOf(c *Config) int64 {
	return p.WindowStart - int64(c.AdjustmentIntervals)*c.IntervalTicks
}

// CreateEvent 由运营方创建事件，返回事件 ID。
func (s *System) CreateEvent(now int64, p EventParams) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateEventParams(&p); err != nil {
		return "", err
	}
	if err := s.checkClock(now); err != nil {
		return "", err
	}
	s.nextID++
	id := fmt.Sprintf("E%d", s.nextID)
	e := &Event{
		ID:          id,
		Params:      p,
		cfg:         &s.cfg,
		invites:     map[string]*Invite{},
		commitments: map[string]*Commitment{},
	}
	s.events[id] = e
	s.eventOrder = append(s.eventOrder, id)
	s.advance(now)
	return id, nil
}

// CancelEvent 由运营方取消事件。窗口开始前取消：释放所有参与者，不考核；
// 窗口开始后取消：窗口截断到取消时刻所在间隔的起点，承诺量按比例折算后仍考核。
func (s *System) CancelEvent(now int64, eventID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	e := s.events[eventID]
	if e == nil {
		return newErr(ErrKindEventState, "事件 %s 不存在", eventID)
	}
	if e.Cancelled || e.Settled {
		return newErr(ErrKindEventState, "事件 %s 已取消或已考核，不得再次取消", eventID)
	}
	if now < e.Params.WindowStart {
		e.Cancelled = true
		for p := range e.commitments {
			delete(s.byPart[p], e.ID)
		}
		e.commitments = map[string]*Commitment{}
	} else {
		e.Cancelled = true
		e.CancelAfterStart = true
		e.CancelTick = now
	}
	s.advance(now)
	return nil
}

// EventState 查询事件在指定时刻的状态（只读，不影响时钟）。
func (s *System) EventState(eventID string, now int64) (State, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.events[eventID]
	if e == nil {
		return StatePublished, false
	}
	return e.state(now), true
}

// AssessmentOf 返回已考核事件的结果（只读）。
func (s *System) AssessmentOf(eventID string) (*Assessment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.events[eventID]
	if e == nil || e.assessment == nil {
		return nil, false
	}
	return e.assessment, true
}

// CommitmentOf 返回参与者在事件上仍具约束力的承诺（只读）。
func (s *System) CommitmentOf(eventID, participant string) (*Commitment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.events[eventID]
	if e == nil {
		return nil, false
	}
	c := e.commitments[participant]
	if c == nil {
		return nil, false
	}
	cp := *c
	return &cp, true
}
