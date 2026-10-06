package staffing

import "fmt"

// AddPosition 注册岗位。编制总数须非负，带宽低端不大于高端。
func (s *Service) AddPosition(now int, spec PositionSpec) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("AddPosition{now:%d spec:%+v}", now, spec)
	defer func() {
		if err != nil {
			s.log("AddPosition", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("AddPosition", input, true, CodeOK, spec.ID, "position registered")
		}
	}()

	if err := validatePosSpec(spec); err != nil {
		return err
	}
	if err := s.advanceClock(now); err != nil {
		return err
	}
	if _, ok := s.positions[spec.ID]; ok {
		return newError(CodeInvalidState, "position %q already exists", spec.ID)
	}

	s.positions[spec.ID] = &Position{
		ID: spec.ID, BandLow: spec.BandLow, BandHigh: spec.BandHigh,
		Headcount: spec.Headcount, Frozen: false,
	}
	s.commitClock(now)
	return nil
}

func validatePosSpec(spec PositionSpec) error {
	if spec.ID == "" {
		return newError(CodeInvalidParam, "position id is empty")
	}
	if spec.Headcount < 0 {
		return newError(CodeInvalidParam, "headcount must be >= 0, got %d", spec.Headcount)
	}
	if spec.BandLow > spec.BandHigh {
		return newError(CodeInvalidParam, "band low %d > band high %d", spec.BandLow, spec.BandHigh)
	}
	return nil
}

// AddCandidate 注册候选人。
func (s *Service) AddCandidate(now int, id string) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("AddCandidate{now:%d id:%q}", now, id)
	defer func() {
		if err != nil {
			s.log("AddCandidate", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("AddCandidate", input, true, CodeOK, id, "candidate registered")
		}
	}()

	if id == "" {
		return newError(CodeInvalidParam, "candidate id is empty")
	}
	if err := s.advanceClock(now); err != nil {
		return err
	}
	if s.candidates[id] {
		return newError(CodeInvalidState, "candidate %q already exists", id)
	}
	s.candidates[id] = true
	s.commitClock(now)
	return nil
}

// AddException 为某岗位某季度登记固定次数的例外审批。
// 同一 (岗位, 季度) 不可重复登记；total 须非负。
func (s *Service) AddException(now int, id, positionID string, quarter, total int) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("AddException{now:%d id:%q pos:%q quarter:%d total:%d}", now, id, positionID, quarter, total)
	defer func() {
		if err != nil {
			s.log("AddException", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("AddException", input, true, CodeOK, id, "exception approval registered")
		}
	}()

	if id == "" || positionID == "" || quarter < 0 || total < 0 {
		return newError(CodeInvalidParam, "invalid exception params: id=%q position=%q quarter=%d total=%d",
			id, positionID, quarter, total)
	}
	if err := s.advanceClock(now); err != nil {
		return err
	}
	if _, ok := s.positions[positionID]; !ok {
		return newError(CodeNotFound, "position %q not found", positionID)
	}
	if _, ok := s.exceptions[id]; ok {
		return newError(CodeInvalidState, "exception %q already exists", id)
	}
	key := s.exceptionKey(positionID, quarter)
	if _, ok := s.exceptionByKey[key]; ok {
		return newError(CodeInvalidState, "exception for %s already registered", key)
	}

	e := &ExceptionApproval{
		ID: id, PositionID: positionID, Quarter: quarter,
		Total: total, Remaining: total,
	}
	s.exceptions[id] = e
	s.exceptionByKey[key] = e
	s.commitClock(now)
	return nil
}

// SetFrozen 冻结（true）/ 恢复开放（false）岗位。重复设置相同状态允许。
func (s *Service) SetFrozen(now int, positionID string, frozen bool) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("SetFrozen{now:%d pos:%q frozen:%v}", now, positionID, frozen)
	defer func() {
		if err != nil {
			s.log("SetFrozen", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("SetFrozen", input, true, CodeOK, "", fmt.Sprintf("frozen=%v; occupancy unaffected", frozen))
		}
	}()

	if positionID == "" {
		return newError(CodeInvalidParam, "position id is empty")
	}
	if err := s.advanceClock(now); err != nil {
		return err
	}
	p, ok := s.positions[positionID]
	if !ok {
		return newError(CodeNotFound, "position %q not found", positionID)
	}
	p.Frozen = frozen
	s.commitClock(now)
	return nil
}

// AdjustHeadcount 调整编制总数。上调总是允许；下调到恰等于已占用允许，更低拒绝。
func (s *Service) AdjustHeadcount(now int, positionID string, total int) (err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	input := fmt.Sprintf("AdjustHeadcount{now:%d pos:%q total:%d}", now, positionID, total)
	defer func() {
		if err != nil {
			s.log("AdjustHeadcount", input, false, ErrCode(err), "", err.Error())
		} else {
			s.log("AdjustHeadcount", input, true, CodeOK, "",
				fmt.Sprintf("headcount=%d occupied=%d", s.positions[positionID].Headcount, s.occupied(positionID)))
		}
	}()

	if positionID == "" || total < 0 {
		return newError(CodeInvalidParam, "invalid params: position=%q total=%d", positionID, total)
	}
	if err := s.advanceClock(now); err != nil {
		return err
	}
	p, ok := s.positions[positionID]
	if !ok {
		return newError(CodeNotFound, "position %q not found", positionID)
	}

	// 编制守恒以“当前实际”为准：先对该岗位所有未决通知做惰性结算。
	// 若下调被拒绝，结算一并回滚——被拒绝操作不改变状态。
	tx := &settleTx{}
	reasons := s.settlePosition(positionID, now, tx)

	occ := s.occupied(positionID)
	if total < occ {
		tx.rollback()
		return newError(CodeHeadcountFull,
			"new headcount %d < occupied %d for position %q", total, occ, positionID)
	}
	p.Headcount = total
	s.commitClock(now)
	for _, r := range reasons {
		s.log("lazy-settle", input, true, CodeOK, "", r)
	}
	return nil
}
