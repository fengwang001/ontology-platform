package dr

import "math"

// Invite 由运营方就一个事件向一名参与者发出邀约并指定请求削减量。
// 同一事件对同一参与者只能邀约一次。
func (s *System) Invite(now int64, eventID, participant string, requested float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if math.IsNaN(requested) || requested < s.cfg.MinCommitment {
		return newErr(ErrKindParam, "请求削减量 %v 小于最小承诺量 %v", requested, s.cfg.MinCommitment)
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	e := s.events[eventID]
	if e == nil {
		return newErr(ErrKindEventState, "事件 %s 不存在", eventID)
	}
	if st := e.state(now); st != StatePublished {
		return newErr(ErrKindEventState, "事件 %s 状态为 %s，不得邀约", eventID, st)
	}
	if e.invites[participant] != nil {
		return newErr(ErrKindEventState, "事件 %s 已邀约过参与者 %s", eventID, participant)
	}
	e.invites[participant] = &Invite{Participant: participant, Requested: requested}
	s.advance(now)
	return nil
}

// Respond 参与者应答：接受（承诺量须不大于请求量且不小于最小承诺量）
// 或拒绝。截止时刻本身视为已过。
func (s *System) Respond(now int64, eventID, participant string, accept bool, amount float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.events[eventID]
	var inv *Invite
	if e != nil {
		inv = e.invites[participant]
	}
	if accept {
		if math.IsNaN(amount) || amount < s.cfg.MinCommitment {
			return newErr(ErrKindParam, "承诺量 %v 小于最小承诺量 %v", amount, s.cfg.MinCommitment)
		}
		if inv != nil && amount > inv.Requested {
			return newErr(ErrKindParam, "承诺量 %v 大于请求量 %v", amount, inv.Requested)
		}
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	if e == nil {
		return newErr(ErrKindEventState, "事件 %s 不存在", eventID)
	}
	if st := e.state(now); st != StatePublished {
		return newErr(ErrKindEventState, "事件 %s 状态为 %s，不得应答", eventID, st)
	}
	if inv == nil || inv.Responded {
		return newErr(ErrKindNotInvited, "参与者 %s 在事件 %s 上无有效邀约", participant, eventID)
	}
	if now >= e.Params.ResponseDeadline {
		return newErr(ErrKindDeadline, "应答截止 %d 已过（当前 %d）", e.Params.ResponseDeadline, now)
	}
	if accept {
		for _, c := range s.commitmentsOf(participant) {
			other := s.events[c.EventID]
			if overlap(other.Params.WindowStart, other.effectiveWindowEnd(),
				e.Params.WindowStart, e.windowEnd()) {
				return newErr(ErrKindEventConflict, "与已接受事件 %s 的窗口重叠", c.EventID)
			}
		}
	}
	inv.Responded = true
	inv.Accepted = accept
	if accept {
		c := &Commitment{
			EventID:     eventID,
			Participant: participant,
			Requested:   inv.Requested,
			Committed:   amount,
		}
		e.commitments[participant] = c
		if s.byPart[participant] == nil {
			s.byPart[participant] = map[string]*Commitment{}
		}
		s.byPart[participant][eventID] = c
	}
	s.advance(now)
	return nil
}

func overlap(start1, end1, start2, end2 int64) bool {
	return start1 < end2 && start2 < end1
}

// Withdraw 参与者在接受后退出：
// 免责退出截止前：不计任何报酬与违约金，承诺移除；
// 免责退出截止之后到窗口结束前：承诺仍在，实际削减按零考核；
// 窗口结束后不得退出。
func (s *System) Withdraw(now int64, eventID, participant string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkClock(now); err != nil {
		return err
	}
	e := s.events[eventID]
	if e == nil {
		return newErr(ErrKindEventState, "事件 %s 不存在", eventID)
	}
	if st := e.state(now); st == StateCancelled || st == StateSettled {
		return newErr(ErrKindEventState, "事件 %s 状态为 %s，不得退出", eventID, st)
	}
	c := e.commitments[participant]
	if c == nil {
		return newErr(ErrKindNotInvited, "参与者 %s 在事件 %s 上无有效承诺", participant, eventID)
	}
	switch {
	case now < e.Params.ExitDeadline:
		delete(e.commitments, participant)
		delete(s.byPart[participant], eventID)
	case now < e.windowEnd():
		c.LateWithdrawn = true
	default:
		return newErr(ErrKindDeadline, "窗口已结束（当前 %d），不得退出", now)
	}
	s.advance(now)
	return nil
}
