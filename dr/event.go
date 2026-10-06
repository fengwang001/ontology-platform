package dr

// CreateEvent 由运营方创建事件。校验顺序：参数非法 > 时钟回退。
func (s *System) CreateEvent(p EventParams, now Tick) *OpError {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p.ID == "" {
		return opErr(ErrParam, "事件 ID 不能为空")
	}
	if _, dup := s.events[p.ID]; dup {
		return opErr(ErrParam, "事件 ID 重复: %s", p.ID)
	}
	if p.Day < 0 {
		return opErr(ErrParam, "事件日不能为负: %d", p.Day)
	}
	dayStart := p.Day * s.ipd
	dayEnd := dayStart + s.ipd
	if p.Start < dayStart || p.End > dayEnd || p.Start >= p.End {
		return opErr(ErrParam, "窗口 [%d,%d) 未落在事件日 %d 内或为空", p.Start, p.End, p.Day)
	}
	if p.Start-s.cfg.AdjustmentIntervals < dayStart {
		return opErr(ErrParam, "调整期越出事件日 %d", p.Day)
	}
	if p.RespondBy < 0 || p.RespondBy >= p.ExitBy {
		return opErr(ErrParam, "应答截止 %d 须早于免责退出截止 %d", p.RespondBy, p.ExitBy)
	}
	if p.ExitBy > s.tickOf(p.Start) {
		return opErr(ErrParam, "免责退出截止 %d 晚于窗口起点 %d", p.ExitBy, s.tickOf(p.Start))
	}
	if p.Price < 0 || p.Penalty < 0 {
		return opErr(ErrParam, "单价不能为负")
	}
	if p.Pass.Den <= 0 || p.Pass.Num < 0 || p.Pass.Num > p.Pass.Den {
		return opErr(ErrParam, "履约合格比例须在 [0,1] 内")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}

	s.events[p.ID] = &Event{
		ID: p.ID, Day: p.Day, Start: p.Start, End: p.End, OrigEnd: p.End,
		RespondBy: p.RespondBy, ExitBy: p.ExitBy,
		Price: p.Price, Penalty: p.Penalty, Pass: p.Pass,
		State: StatePublished,
	}
	return nil
}

// Advance 沿状态机推进一步：已发布→进行中→已结束。
func (s *System) Advance(eventID string, now Tick) *OpError {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(now); err != nil {
		return err
	}
	e, err := s.getEvent(eventID)
	if err != nil {
		return err
	}
	switch e.State {
	case StatePublished:
		if now < s.tickOf(e.Start) {
			return opErr(ErrState, "窗口尚未开始，不能进入进行中")
		}
		e.State = StateInProgress
	case StateInProgress:
		if now < s.tickOf(e.End) {
			return opErr(ErrState, "窗口尚未结束，不能进入已结束")
		}
		e.State = StateEnded
	default:
		return opErr(ErrState, "状态 %s 不允许推进", e.State)
	}
	return nil
}

// Cancel 取消事件。窗口开始前取消：释放所有参与者，不考核；
// 窗口开始后取消：窗口截断到取消时刻所在间隔的起点，事件转为已取消，
// 之后仍可对其考核（承诺量按截断比例折算）。
func (s *System) Cancel(eventID string, now Tick) *OpError {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkClock(now); err != nil {
		return err
	}
	e, err := s.getEvent(eventID)
	if err != nil {
		return err
	}
	switch e.State {
	case StatePublished, StateInProgress, StateEnded:
	default:
		return opErr(ErrState, "状态 %s 不允许取消", e.State)
	}

	if now < s.tickOf(e.Start) {
		e.State = StateCancelled
		for _, c := range s.byEvent[eventID] {
			if c.Status == StAccepted || c.Status == StWithdrawnLate {
				c.Status = StReleased
				s.deactivate(c)
			}
		}
		return nil
	}

	truncEnd := s.intervalOf(now)
	if truncEnd > e.End {
		truncEnd = e.End
	}
	if truncEnd < e.Start {
		truncEnd = e.Start
	}
	e.End = truncEnd
	e.Truncated = true
	e.State = StateCancelled
	return nil
}

// deactivate 将承诺移出生效集合，并释放其占用的资格日排除计数。
func (s *System) deactivate(c *Commitment) {
	if m, ok := s.active[c.Participant]; ok {
		delete(m, c.EventID)
	}
	e := s.events[c.EventID]
	if days, ok := s.excluded[c.Participant]; ok {
		if days[e.Day] > 1 {
			days[e.Day]--
		} else {
			delete(days, e.Day)
		}
	}
}
