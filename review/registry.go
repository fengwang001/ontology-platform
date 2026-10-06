package review

import "time"

// AddReviewer 将一位评委登记入库。
func (s *Service) AddReviewer(r Reviewer) error {
	if r.ID <= 0 || r.Unit == "" || r.Group == "" {
		return errInvalid("评委编号须为正整数且单位、专业组不可为空: %+v", r)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.reviewers[r.ID]; ok {
		return errInvalid("评委编号重复: %d", r.ID)
	}
	cp := r
	s.reviewers[r.ID] = &cp
	return nil
}

// AddApplicant 登记申报人。
func (s *Service) AddApplicant(a Applicant) error {
	if a.ID <= 0 || a.Unit == "" {
		return errInvalid("申报人编号须为正整数且单位不可为空: %+v", a)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.applicants[a.ID]; ok {
		return errInvalid("申报人编号重复: %d", a.ID)
	}
	cp := a
	s.applicants[a.ID] = &applicantState{
		applicant: &cp,
		relations: map[int]struct{}{},
		requests:  map[int]struct{}{},
		autoAvoid: map[int]struct{}{},
	}
	return nil
}

// RegisterRelation 登记直接亲属/师生关系。关系对称且仅限直接关系：
// 每次调用只在申报人与评委之间建立一条直接边，双向写入；
// 重复登记为幂等操作。登记对进行中评审立即生效（由各评审自行发现并替补）。
func (s *Service) RegisterRelation(at time.Time, applicantID, reviewerID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 错误优先级：参数非法 > 时钟回退 > 不存在。
	if applicantID <= 0 || reviewerID <= 0 {
		return errInvalid("编号须为正整数: 申报人=%d 评委=%d", applicantID, reviewerID)
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	app, ok := s.applicants[applicantID]
	if !ok {
		return errNotFound("申报人不存在: %d", applicantID)
	}
	if _, ok := s.reviewers[reviewerID]; !ok {
		return errNotFound("评委不存在: %d", reviewerID)
	}
	s.advanceClock(at)
	app.relations[reviewerID] = struct{}{}
	return nil
}

// AcceptRecusalRequest 受理一条"申报人要求某评委回避"的申请。
// 受理立即生效并对进行中评审产生影响；重复受理为幂等操作。
func (s *Service) AcceptRecusalRequest(at time.Time, applicantID, reviewerID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if applicantID <= 0 || reviewerID <= 0 {
		return errInvalid("编号须为正整数: 申报人=%d 评委=%d", applicantID, reviewerID)
	}
	if err := s.checkClock(at); err != nil {
		return err
	}
	app, ok := s.applicants[applicantID]
	if !ok {
		return errNotFound("申报人不存在: %d", applicantID)
	}
	if _, ok := s.reviewers[reviewerID]; !ok {
		return errNotFound("评委不存在: %d", reviewerID)
	}
	s.advanceClock(at)
	app.requests[reviewerID] = struct{}{}
	return nil
}

// IsRecused 报告评委 reviewerID 对申报人 applicantID 是否须回避，
// 以及全部命中的回避来源。
// 命中任一来源即须回避：同单位 / 登记关系 / 已受理回避申请 / 作废评审前科。
func (s *Service) IsRecused(applicantID, reviewerID int) (bool, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if applicantID <= 0 || reviewerID <= 0 {
		return false, nil, errInvalid("编号须为正整数: 申报人=%d 评委=%d", applicantID, reviewerID)
	}
	app, ok := s.applicants[applicantID]
	if !ok {
		return false, nil, errNotFound("申报人不存在: %d", applicantID)
	}
	rev, ok := s.reviewers[reviewerID]
	if !ok {
		return false, nil, errNotFound("评委不存在: %d", reviewerID)
	}
	src := s.recusalSources(app, rev)
	return len(src) > 0, src, nil
}

// recusalSources 调用时须持锁。
func (s *Service) recusalSources(app *applicantState, rev *Reviewer) []string {
	var src []string
	if app.applicant.Unit == rev.Unit {
		src = append(src, "同单位")
	}
	if _, ok := app.relations[rev.ID]; ok {
		src = append(src, "登记关系")
	}
	if _, ok := app.requests[rev.ID]; ok {
		src = append(src, "回避申请")
	}
	if _, ok := app.autoAvoid[rev.ID]; ok {
		src = append(src, "作废前科")
	}
	return src
}
