package review

import "time"

// AcceptObjection 在公示期内受理一次异议。公示期为自宣布时刻起整整
// 七个自然日的左闭右开区间 [宣布时刻, 结束时刻)；结束时刻本身不再受理。
// 受理至多一次，重复受理报状态不允许。
func (s *Service) AcceptObjection(at time.Time, reviewID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if reviewID <= 0 {
		return errInvalid("评审编号须为正整数: %d", reviewID)
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return errNotFound("评审不存在: %d", reviewID)
	}
	if r.status != StatusPublicity {
		return errState("评审 %d 当前为 %s，仅公示中可受理异议", reviewID, statusName(r.status))
	}
	if r.objection {
		return errState("评审 %d 的异议已受理，不可重复受理", reviewID)
	}
	if !at.Before(r.publicityEnd) {
		return errState("公示已于 %s 结束，结束时刻起不再受理异议", formatTime(r.publicityEnd))
	}
	s.advanceClock(at)
	r.objection = true
	r.objectionTime = at
	return nil
}

// AdjudicateObjection 对已受理异议作出裁定。upheld=true 裁定成立：
// 评审整体作废，原评委（当前版本全体）自动对该申报人回避，评委释放。
// upheld=false 裁定不成立：评审继续公示，待公示结束时刻终局生效。
// 裁定仅能发生一次。
func (s *Service) AdjudicateObjection(at time.Time, reviewID int, upheld bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if reviewID <= 0 {
		return errInvalid("评审编号须为正整数: %d", reviewID)
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return errNotFound("评审不存在: %d", reviewID)
	}
	if r.status != StatusPublicity {
		return errState("评审 %d 当前为 %s，无待裁定异议", reviewID, statusName(r.status))
	}
	if !r.objection {
		return errState("评审 %d 尚未受理异议，无从裁定", reviewID)
	}
	if r.adjudicated {
		return errState("评审 %d 的异议已裁定，不可重复裁定", reviewID)
	}

	r.adjudicated = true
	if !upheld {
		// 裁定不成立：保持公示中，终局只能在公示结束时刻之后生效。
		s.advanceClock(at)
		return nil
	}

	// 裁定成立：评审整体作废；当前版本全体原评委对该申报人永久回避。
	app := s.applicants[r.applicant]
	for id := range r.panel {
		app.autoAvoid[id] = struct{}{}
		delete(s.occupied, id)
	}
	r.status = StatusVoid
	r.objectionUpheld = true
	s.advanceClock(at)
	return nil
}

// Finalize 在公示期满后使结果终局生效。仅当 at >= 公示结束时刻且：
// 无异议，或异议经裁定不成立时，结论方可生效。返回终局状态。
// 结束时刻之前调用报状态不允许；终局仅发生一次。
func (s *Service) Finalize(at time.Time, reviewID int) (ReviewStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if reviewID <= 0 {
		return 0, errInvalid("评审编号须为正整数: %d", reviewID)
	}
	if err := s.checkClock(at); err != nil {
		return 0, err
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return 0, errNotFound("评审不存在: %d", reviewID)
	}
	if r.status != StatusPublicity {
		return 0, errState("评审 %d 当前为 %s，不能终局生效", reviewID, statusName(r.status))
	}
	if at.Before(r.publicityEnd) {
		return 0, errState("公示 %s 才结束，结束前结果不可视为终局", formatTime(r.publicityEnd))
	}
	if r.objection && !r.adjudicated {
		return 0, errState("评审 %d 已受理异议但尚未裁定，不能终局生效", reviewID)
	}
	if r.objectionUpheld {
		return 0, errState("评审 %d 已因异议成立而作废", reviewID)
	}

	r.status = r.tentative
	for id := range r.panel {
		delete(s.occupied, id)
	}
	s.advanceClock(at)
	return r.status, nil
}
