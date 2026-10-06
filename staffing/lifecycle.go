package staffing

import "fmt"

// touchOffer 是所有“针对某份通知”的操作的统一入口：
// 参数 -> 时钟 -> 定位 -> 惰性结算（全部记录在 tx 中）。
// 业务动作成功时由调用方 commitTouch 物化并推进时钟；
// 业务动作失败时调用 tx.rollback()，被拒绝操作不留下任何副作用。
func (s *Service) touchOffer(now int, offerID int64) (*Offer, *settleTx, string, error) {
	if offerID <= 0 {
		return nil, nil, "", newError(CodeInvalidParam, "offer id must be > 0, got %d", offerID)
	}
	if err := s.advanceClock(now); err != nil {
		return nil, nil, "", err
	}
	o, ok := s.offers[offerID]
	if !ok {
		return nil, nil, "", newError(CodeNotFound, "offer %d not found", offerID)
	}
	tx := &settleTx{}
	reason := s.settleOffer(o, now, tx)
	return o, tx, reason, nil
}

func (s *Service) commitTouch(tx *settleTx, now int, input, settleReason string) {
	if settleReason != "" {
		s.log("lazy-settle", input, true, CodeOK, "", settleReason)
	}
	s.commitClock(now)
}

// rejectTouch 回滚惰性结算并返回业务错误。
func (s *Service) rejectTouch(tx *settleTx, err *Error) error {
	tx.rollback()
	return err
}

// Respond 候选人答复。accept=true 时须约定不早于 now 的入职日 entryDate。
func (s *Service) Respond(now int, offerID int64, accept bool, entryDate int) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("Respond{now:%d offer:%d accept:%v entryDate:%d}", now, offerID, accept, entryDate)
	defer func() {
		if err != nil {
			s.log("Respond", input, false, ErrCode(err), "", err.Error())
		} else if accept {
			s.log("Respond", input, true, CodeOK, "", "offer ACCEPTED, headcount remains occupied")
		} else {
			s.log("Respond", input, true, CodeOK, "", "offer REJECTED, headcount released, cooldown starts")
		}
	}()

	if accept && entryDate < 0 {
		return newError(CodeInvalidParam, "entryDate must be >= 0 on accept, got %d", entryDate)
	}
	o, tx, settleReason, err := s.touchOffer(now, offerID)
	if err != nil {
		return err
	}

	switch o.Status {
	case StatusExpired, StatusAbandoned:
		return s.rejectTouch(tx, newError(CodeExpired, "offer %d is %s at day %d", offerID, o.Status, now))
	case StatusPending:
	default:
		return s.rejectTouch(tx, newError(CodeInvalidState, "offer %d is %s, cannot respond", offerID, o.Status))
	}

	if accept && entryDate < now {
		return s.rejectTouch(tx, newError(CodeInvalidParam, "entryDate %d before respond day %d", entryDate, now))
	}

	if accept {
		o.Status = StatusAccepted
		o.RespondedAt = now
		o.EntryDate = entryDate
		s.commitTouch(tx, now, input, settleReason)
		return nil
	}

	s.releasePending(o, tx)
	o.Status = StatusRejected
	o.RespondedAt = now
	s.markBlock(o.CandidateID, o.PositionID, now, tx)
	s.commitTouch(tx, now, input, settleReason)
	return nil
}

// Onboard 办理入职：仅 ACCEPTED 通知可办理；
// entryDate <= now <= entryDate+grace 合法（恰等于上限仍可入职）。
func (s *Service) Onboard(now int, offerID int64) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("Onboard{now:%d offer:%d}", now, offerID)
	defer func() {
		if err != nil {
			s.log("Onboard", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("Onboard", input, true, CodeOK, "", "offer ONBOARDED: pending occupancy converted to onboarded")
		}
	}()

	o, tx, settleReason, err := s.touchOffer(now, offerID)
	if err != nil {
		return err
	}
	switch o.Status {
	case StatusExpired, StatusAbandoned:
		return s.rejectTouch(tx, newError(CodeExpired, "offer %d is %s at day %d", offerID, o.Status, now))
	case StatusAccepted:
	default:
		return s.rejectTouch(tx, newError(CodeInvalidState, "offer %d is %s, cannot onboard", offerID, o.Status))
	}

	if now < o.EntryDate {
		return s.rejectTouch(tx, newError(CodeInvalidState,
			"cannot onboard offer %d before entry date %d (now %d)", offerID, o.EntryDate, now))
	}

	s.pendingCnt[o.PositionID]--
	delete(s.pendingByPos[o.PositionID], o.ID)
	delete(s.candidatePending, o.CandidateID)
	s.onboardedCnt[o.PositionID]++
	s.candidateOnboarded[o.CandidateID] = o.ID
	o.Status = StatusOnboarded
	o.OnboardedAt = now
	s.commitTouch(tx, now, input, settleReason)
	return nil
}

// Withdraw 在候选人答复前撤回未决通知；已接受不可撤回。不触发冷却。
func (s *Service) Withdraw(now int, offerID int64) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("Withdraw{now:%d offer:%d}", now, offerID)
	defer func() {
		if err != nil {
			s.log("Withdraw", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("Withdraw", input, true, CodeOK, "", "offer WITHDRAWN, headcount released, no cooldown")
		}
	}()

	o, tx, settleReason, err := s.touchOffer(now, offerID)
	if err != nil {
		return err
	}
	switch o.Status {
	case StatusExpired, StatusAbandoned:
		return s.rejectTouch(tx, newError(CodeExpired, "offer %d is %s at day %d", offerID, o.Status, now))
	case StatusPending:
		s.releasePending(o, tx)
		o.Status = StatusWithdrawn
		s.commitTouch(tx, now, input, settleReason)
		return nil
	case StatusAccepted:
		return s.rejectTouch(tx, newError(CodeInvalidState,
			"offer %d already accepted; use Cancel (negotiated) instead of Withdraw", offerID))
	default:
		return s.rejectTouch(tx, newError(CodeInvalidState, "offer %d is %s, cannot withdraw", offerID, o.Status))
	}
}

// Cancel 对已接受通知协商取消，释放占用并记为取消；不触发冷却。
func (s *Service) Cancel(now int, offerID int64) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("Cancel{now:%d offer:%d}", now, offerID)
	defer func() {
		if err != nil {
			s.log("Cancel", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("Cancel", input, true, CodeOK, "", "offer CANCELED, headcount released, no cooldown")
		}
	}()

	o, tx, settleReason, err := s.touchOffer(now, offerID)
	if err != nil {
		return err
	}
	switch o.Status {
	case StatusExpired, StatusAbandoned:
		return s.rejectTouch(tx, newError(CodeExpired, "offer %d is %s at day %d", offerID, o.Status, now))
	case StatusAccepted:
		s.releasePending(o, tx)
		o.Status = StatusCanceled
		o.CanceledAt = now
		s.commitTouch(tx, now, input, settleReason)
		return nil
	case StatusPending:
		return s.rejectTouch(tx, newError(CodeInvalidState,
			"offer %d not yet responded; use Withdraw instead of Cancel", offerID))
	default:
		return s.rejectTouch(tx, newError(CodeInvalidState, "offer %d is %s, cannot cancel", offerID, o.Status))
	}
}

// Leave 已入职人员离职，释放一个编制；不可使在岗人数为负。
func (s *Service) Leave(now int, candidateID string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("Leave{now:%d candidate:%q}", now, candidateID)
	defer func() {
		if err != nil {
			s.log("Leave", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("Leave", input, true, CodeOK, "", "employee left, one onboarded headcount released")
		}
	}()

	if candidateID == "" {
		return newError(CodeInvalidParam, "candidate id is empty")
	}
	if err := s.advanceClock(now); err != nil {
		return err
	}
	id, ok := s.candidateOnboarded[candidateID]
	if !ok {
		return newError(CodeInvalidState, "candidate %q is not onboarded", candidateID)
	}
	o := s.offers[id]
	if o.Status != StatusOnboarded {
		return newError(CodeInvalidState, "offer %d is %s, cannot leave", id, o.Status)
	}
	if s.onboardedCnt[o.PositionID] <= 0 {
		return newError(CodeInvalidState, "onboarded count for %q is already 0", o.PositionID)
	}

	s.onboardedCnt[o.PositionID]--
	delete(s.candidateOnboarded, candidateID)
	o.LeftAt = now
	s.commitClock(now)
	return nil
}

// GetOffer 查询通知。查询也是“触及”：成功的查询会物化惰性过期/放弃。
func (s *Service) GetOffer(now int, offerID int64) (out Offer, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("GetOffer{now:%d offer:%d}", now, offerID)
	defer func() {
		if err != nil {
			s.log("GetOffer", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("GetOffer", input, true, CodeOK, fmt.Sprintf("status:%s", out.Status), "offer read")
		}
	}()

	o, tx, settleReason, err := s.touchOffer(now, offerID)
	if err != nil {
		return Offer{}, err
	}
	out = *o
	s.commitTouch(tx, now, input, settleReason)
	return out, nil
}

// Occupancy 返回岗位 (已占用, 在岗, 未决)。查询触发该岗位全部未决通知的惰性结算。
func (s *Service) Occupancy(now int, positionID string) (occupied, onboarded, pending int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("Occupancy{now:%d pos:%q}", now, positionID)
	defer func() {
		if err != nil {
			s.log("Occupancy", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("Occupancy", input, true, CodeOK,
				fmt.Sprintf("occupied:%d onboarded:%d pending:%d", occupied, onboarded, pending), "occupancy read")
		}
	}()

	if positionID == "" {
		return 0, 0, 0, newError(CodeInvalidParam, "position id is empty")
	}
	if err := s.advanceClock(now); err != nil {
		return 0, 0, 0, err
	}
	if _, ok := s.positions[positionID]; !ok {
		return 0, 0, 0, newError(CodeNotFound, "position %q not found", positionID)
	}
	tx := &settleTx{}
	reasons := s.settlePosition(positionID, now, tx)
	for _, r := range reasons {
		s.log("lazy-settle", input, true, CodeOK, "", r)
	}
	s.commitClock(now)
	return s.occupied(positionID), s.onboardedCnt[positionID], s.pendingCnt[positionID], nil
}
