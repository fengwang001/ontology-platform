package dr

// Invite 运营方对一个事件向一名参与者发出邀约。
// 同一事件对同一参与者只能邀约一次。
func (s *System) Invite(eventID, participant string, requested int64, now Tick) *OpError {
	s.mu.Lock()
	defer s.mu.Unlock()

	if participant == "" {
		return opErr(ErrParam, "参与者不能为空")
	}
	if requested < s.cfg.MinCommitment || requested <= 0 {
		return opErr(ErrParam, "请求削减量 %d 小于最小承诺量 %d", requested, s.cfg.MinCommitment)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	e, err := s.getEvent(eventID)
	if err != nil {
		return err
	}
	if e.State != StatePublished {
		return opErr(ErrState, "事件状态 %s 不允许邀约", e.State)
	}
	m := s.byEvent[eventID]
	if m == nil {
		m = map[string]*Commitment{}
		s.byEvent[eventID] = m
	}
	if _, dup := m[participant]; dup {
		return opErr(ErrState, "事件 %s 已邀约过参与者 %s", eventID, participant)
	}
	m[participant] = &Commitment{
		EventID: eventID, Participant: participant,
		Requested: requested, Status: StInvited,
	}
	return nil
}

// Accept 参与者在应答截止前接受邀约。
// 校验顺序：参数 > 时钟 > 事件状态 > 未邀约 > 已过截止 > 事件冲突。
func (s *System) Accept(eventID, participant string, amount int64, now Tick) *OpError {
	s.mu.Lock()
	defer s.mu.Unlock()

	if participant == "" {
		return opErr(ErrParam, "参与者不能为空")
	}
	if amount <= 0 || amount < s.cfg.MinCommitment {
		return opErr(ErrParam, "承诺量 %d 小于最小承诺量 %d", amount, s.cfg.MinCommitment)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	e, err := s.getEvent(eventID)
	if err != nil {
		return err
	}
	if e.State != StatePublished {
		return opErr(ErrState, "事件状态 %s 不允许应答", e.State)
	}
	c := s.byEvent[eventID][participant]
	if c == nil {
		return opErr(ErrNotInvited, "参与者 %s 未被事件 %s 邀约", participant, eventID)
	}
	if c.Status != StInvited {
		return opErr(ErrState, "承诺状态 %s 不允许重复应答", c.Status)
	}
	if amount > c.Requested {
		return opErr(ErrParam, "承诺量 %d 大于请求量 %d", amount, c.Requested)
	}
	if now >= e.RespondBy {
		return opErr(ErrDeadline, "时刻 %d 已过应答截止 %d", now, e.RespondBy)
	}
	if other := s.findOverlap(participant, e); other != "" {
		return opErr(ErrConflict, "与已接受事件 %s 窗口重叠", other)
	}

	c.Status = StAccepted
	c.Committed = amount
	s.activate(c, e)
	return nil
}

// Reject 参与者在应答截止前拒绝邀约。
func (s *System) Reject(eventID, participant string, now Tick) *OpError {
	s.mu.Lock()
	defer s.mu.Unlock()

	if participant == "" {
		return opErr(ErrParam, "参与者不能为空")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	e, err := s.getEvent(eventID)
	if err != nil {
		return err
	}
	c := s.byEvent[eventID][participant]
	if c == nil {
		return opErr(ErrNotInvited, "参与者 %s 未被事件 %s 邀约", participant, eventID)
	}
	if c.Status != StInvited {
		return opErr(ErrState, "承诺状态 %s 不允许拒绝", c.Status)
	}
	if now >= e.RespondBy {
		return opErr(ErrDeadline, "时刻 %d 已过应答截止 %d", now, e.RespondBy)
	}
	c.Status = StRejected
	return nil
}

// Withdraw 参与者退出已接受的事件。
// 免责退出截止前：不计报酬与违约金；截止后至窗口结束前：承诺仍在，
// 实际削减按零考核；窗口结束后不得退出。
func (s *System) Withdraw(eventID, participant string, now Tick) *OpError {
	s.mu.Lock()
	defer s.mu.Unlock()

	if participant == "" {
		return opErr(ErrParam, "参与者不能为空")
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	e, err := s.getEvent(eventID)
	if err != nil {
		return err
	}
	c := s.byEvent[eventID][participant]
	if c == nil {
		return opErr(ErrNotInvited, "参与者 %s 未被事件 %s 邀约", participant, eventID)
	}
	if c.Status != StAccepted {
		return opErr(ErrState, "承诺状态 %s 不允许退出", c.Status)
	}
	if e.State != StatePublished && e.State != StateInProgress {
		return opErr(ErrState, "事件状态 %s 不允许退出", e.State)
	}
	if now >= s.tickOf(e.End) {
		return opErr(ErrState, "窗口已结束，不得退出")
	}

	if now < e.ExitBy {
		c.Status = StWithdrawnEarly
		// 免责退出：移出生效集合，但资格日排除保留（曾接受过该事件）。
		if am, ok := s.active[participant]; ok {
			delete(am, eventID)
		}
		return nil
	}
	c.Status = StWithdrawnLate
	return nil
}

// activate 登记生效承诺并占用事件日的资格日排除计数。
func (s *System) activate(c *Commitment, e *Event) {
	if s.active[c.Participant] == nil {
		s.active[c.Participant] = map[string]*Commitment{}
	}
	s.active[c.Participant][c.EventID] = c
	if s.excluded[c.Participant] == nil {
		s.excluded[c.Participant] = map[int]int{}
	}
	s.excluded[c.Participant][e.Day]++
}

// findOverlap 在参与者生效中的承诺里查找与事件 e 窗口重叠者，
// 返回冲突事件 ID；无冲突返回空串。开销只与该参与者自身承诺数相关。
func (s *System) findOverlap(participant string, e *Event) string {
	for id := range s.active[participant] {
		other := s.events[id]
		if e.Start < other.End && other.Start < e.End {
			return id
		}
	}
	return ""
}
