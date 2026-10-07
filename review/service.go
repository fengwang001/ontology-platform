package review

import (
	"slices"
	"sync"
	"time"
)

// Service 职称评审会务服务。所有公开操作可并发调用，
// 内部以单一互斥锁串行化，结果等价于某个串行顺序；
// 配合逻辑时钟，相同操作序列重放得到完全相同的结果。
type Service struct {
	mu            sync.Mutex
	now           time.Time
	experts       map[int64]*Expert
	expertOrder   []int64
	applicants    map[int64]*Applicant
	reviews       map[int64]*Review
	reviewOrder   []int64
	nextReviewID  int64
	recusalChecks int64
}

// NewService 创建服务，start 为逻辑时钟起点。
func NewService(start time.Time) *Service {
	return &Service{
		now:          start,
		experts:      map[int64]*Expert{},
		applicants:   map[int64]*Applicant{},
		reviews:      map[int64]*Review{},
		nextReviewID: 1,
	}
}

// Now 返回当前逻辑时钟。
func (s *Service) Now() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

// Stats 返回内部计数器（用于复杂度可验证性）。
func (s *Service) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{RecusalChecks: s.recusalChecks}
}

func insertSorted(xs []int64, x int64) []int64 {
	i, _ := slices.BinarySearch(xs, x)
	return slices.Insert(xs, i, x)
}

func sortedKeys(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// AddExpert 登记评委。
func (s *Service) AddExpert(at time.Time, id int64, unit, group string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if unit == "" || group == "" {
		return newErr(ErrInvalidParam, "AddExpert", "unit and group must be non-empty")
	}
	if at.Before(s.now) {
		return newErr(ErrClockRollback, "AddExpert", "at %s before now %s", at, s.now)
	}
	if _, ok := s.experts[id]; ok {
		return newErr(ErrStateNotAllowed, "AddExpert", "expert %d already exists", id)
	}
	s.experts[id] = &Expert{ID: id, Unit: unit, Group: group}
	s.expertOrder = insertSorted(s.expertOrder, id)
	s.now = at
	return nil
}

// AddApplicant 登记申报人。
func (s *Service) AddApplicant(at time.Time, id int64, unit string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if unit == "" {
		return newErr(ErrInvalidParam, "AddApplicant", "unit must be non-empty")
	}
	if at.Before(s.now) {
		return newErr(ErrClockRollback, "AddApplicant", "at %s before now %s", at, s.now)
	}
	if _, ok := s.applicants[id]; ok {
		return newErr(ErrStateNotAllowed, "AddApplicant", "applicant %d already exists", id)
	}
	s.applicants[id] = &Applicant{
		ID:              id,
		Unit:            unit,
		Relations:       map[int64]bool{},
		RecusalPending:  map[int64]bool{},
		RecusalAccepted: map[int64]bool{},
		VoidedExperts:   map[int64]bool{},
	}
	s.now = at
	return nil
}

// AddRelation 登记申报人与评委间的亲属/师生关系（对称、仅直接关系）。
// 若该申报人存在进行中的评审，立即触发回避检查与替补。
func (s *Service) AddRelation(at time.Time, applicantID, expertID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.Before(s.now) {
		return newErr(ErrClockRollback, "AddRelation", "at %s before now %s", at, s.now)
	}
	app, ok := s.applicants[applicantID]
	if !ok {
		return newErr(ErrNotFound, "AddRelation", "applicant %d not found", applicantID)
	}
	if _, ok := s.experts[expertID]; !ok {
		return newErr(ErrNotFound, "AddRelation", "expert %d not found", expertID)
	}
	if app.Relations[expertID] {
		return newErr(ErrStateNotAllowed, "AddRelation", "relation already registered")
	}
	app.Relations[expertID] = true
	s.recheckApplicant(app, at)
	s.now = at
	return nil
}

// ApplyRecusal 申报人对评委提出回避申请（尚未受理）。
func (s *Service) ApplyRecusal(at time.Time, applicantID, expertID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.Before(s.now) {
		return newErr(ErrClockRollback, "ApplyRecusal", "at %s before now %s", at, s.now)
	}
	app, ok := s.applicants[applicantID]
	if !ok {
		return newErr(ErrNotFound, "ApplyRecusal", "applicant %d not found", applicantID)
	}
	if _, ok := s.experts[expertID]; !ok {
		return newErr(ErrNotFound, "ApplyRecusal", "expert %d not found", expertID)
	}
	if app.RecusalPending[expertID] || app.RecusalAccepted[expertID] {
		return newErr(ErrStateNotAllowed, "ApplyRecusal", "recusal application already exists")
	}
	app.RecusalPending[expertID] = true
	s.now = at
	return nil
}

// AcceptRecusal 受理回避申请。若该申报人存在进行中的评审，
// 立即触发回避检查与替补。
func (s *Service) AcceptRecusal(at time.Time, applicantID, expertID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.Before(s.now) {
		return newErr(ErrClockRollback, "AcceptRecusal", "at %s before now %s", at, s.now)
	}
	app, ok := s.applicants[applicantID]
	if !ok {
		return newErr(ErrNotFound, "AcceptRecusal", "applicant %d not found", applicantID)
	}
	if _, ok := s.experts[expertID]; !ok {
		return newErr(ErrNotFound, "AcceptRecusal", "expert %d not found", expertID)
	}
	if !app.RecusalPending[expertID] {
		return newErr(ErrStateNotAllowed, "AcceptRecusal", "no pending recusal application")
	}
	delete(app.RecusalPending, expertID)
	app.RecusalAccepted[expertID] = true
	s.recheckApplicant(app, at)
	s.now = at
	return nil
}

// CreateReview 为申报人抽取评委组并创建评审。
// 返回评审编号与唯一确定的评委组（编号升序、字典序最小）。
// 评委不足时报错且不占用任何评委、不改变任何状态。
func (s *Service) CreateReview(at time.Time, applicantID int64, n int, groupMins map[string]int) (int64, []int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n <= 0 || n%2 == 0 {
		return 0, nil, newErr(ErrInvalidParam, "CreateReview", "n must be a positive odd number, got %d", n)
	}
	sum := 0
	for g, m := range groupMins {
		if g == "" {
			return 0, nil, newErr(ErrInvalidParam, "CreateReview", "group name must be non-empty")
		}
		if m < 0 {
			return 0, nil, newErr(ErrInvalidParam, "CreateReview", "group minimum must be >= 0")
		}
		sum += m
	}
	if sum > n {
		return 0, nil, newErr(ErrInvalidParam, "CreateReview", "sum of group minimums %d exceeds n %d", sum, n)
	}
	if at.Before(s.now) {
		return 0, nil, newErr(ErrClockRollback, "CreateReview", "at %s before now %s", at, s.now)
	}
	app, ok := s.applicants[applicantID]
	if !ok {
		return 0, nil, newErr(ErrNotFound, "CreateReview", "applicant %d not found", applicantID)
	}
	panel, ok := s.draw(app, n, groupMins)
	if !ok {
		return 0, nil, newErr(ErrInsufficientExperts, "CreateReview", "cannot form a panel of %d satisfying group minimums", n)
	}
	mins := make(map[string]int, len(groupMins))
	for g, m := range groupMins {
		mins[g] = m
	}
	r := &Review{
		ID:          s.nextReviewID,
		ApplicantID: applicantID,
		N:           n,
		GroupMins:   mins,
		Versions: []*PanelVersion{{
			Index:   0,
			Members: panel,
			Reason:  "initial draw",
			At:      at,
		}},
		rounds: 1,
	}
	s.nextReviewID++
	s.reviews[r.ID] = r
	s.reviewOrder = append(s.reviewOrder, r.ID)
	s.now = at
	return r.ID, slices.Clone(panel), nil
}

// Vote 评委在指定轮次投票。每轮每人只能投一次且不可更改。
func (s *Service) Vote(at time.Time, reviewID, expertID int64, round int, choice Choice) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if round < 1 || round > 2 {
		return newErr(ErrInvalidParam, "Vote", "round must be 1 or 2, got %d", round)
	}
	if !choice.valid() {
		return newErr(ErrInvalidParam, "Vote", "invalid choice %d", choice)
	}
	if at.Before(s.now) {
		return newErr(ErrClockRollback, "Vote", "at %s before now %s", at, s.now)
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return newErr(ErrNotFound, "Vote", "review %d not found", reviewID)
	}
	if r.Aborted || r.Voided || r.finalEntry() != nil {
		return newErr(ErrStateNotAllowed, "Vote", "review %d is not open for voting", reviewID)
	}
	if round > r.rounds {
		return newErr(ErrStateNotAllowed, "Vote", "round %d is not open", round)
	}
	if !slices.Contains(r.members(), expertID) {
		return newErr(ErrNoPermission, "Vote", "expert %d is not a current panelist of review %d", expertID, reviewID)
	}
	for _, v := range r.Votes {
		if v.Round == round && v.ExpertID == expertID && v.VoidedAt < 0 {
			return newErr(ErrDuplicateVote, "Vote", "expert %d already voted in round %d", expertID, round)
		}
	}
	r.Votes = append(r.Votes, &VoteRecord{
		ExpertID: expertID,
		Round:    round,
		Choice:   choice,
		Version:  len(r.Versions) - 1,
		At:       at,
		VoidedAt: -1,
	})
	s.settleAndTrail(r, at)
	s.now = at
	return nil
}

// FileObjection 公示期内受理异议。公示结束时刻本身不再受理。
func (s *Service) FileObjection(at time.Time, reviewID int64, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.Before(s.now) {
		return newErr(ErrClockRollback, "FileObjection", "at %s before now %s", at, s.now)
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return newErr(ErrNotFound, "FileObjection", "review %d not found", reviewID)
	}
	if r.Aborted || r.Voided {
		return newErr(ErrStateNotAllowed, "FileObjection", "review %d is closed", reviewID)
	}
	fin := r.finalEntry()
	if fin == nil {
		return newErr(ErrStateNotAllowed, "FileObjection", "review %d has no announced result", reviewID)
	}
	if !at.Before(fin.At.Add(PublicityDuration)) {
		return newErr(ErrStateNotAllowed, "FileObjection", "publicity period has ended")
	}
	if r.Objection != nil {
		return newErr(ErrStateNotAllowed, "FileObjection", "objection already filed")
	}
	r.Objection = &Objection{Reason: reason, FiledAt: at}
	s.now = at
	return nil
}

// RuleObjection 裁定异议。成立则评审整体作废，原评委自动回避该申报人。
func (s *Service) RuleObjection(at time.Time, reviewID int64, upheld bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if at.Before(s.now) {
		return newErr(ErrClockRollback, "RuleObjection", "at %s before now %s", at, s.now)
	}
	r, ok := s.reviews[reviewID]
	if !ok {
		return newErr(ErrNotFound, "RuleObjection", "review %d not found", reviewID)
	}
	if r.Aborted || r.Voided {
		return newErr(ErrStateNotAllowed, "RuleObjection", "review %d is closed", reviewID)
	}
	if r.Objection == nil || r.Objection.Ruled {
		return newErr(ErrStateNotAllowed, "RuleObjection", "no pending objection to rule")
	}
	r.Objection.Ruled = true
	r.Objection.Upheld = upheld
	r.Objection.RuledAt = at
	if upheld {
		r.Voided = true
		app := s.applicants[r.ApplicantID]
		for _, v := range r.Versions {
			for _, m := range v.Members {
				app.VoidedExperts[m] = true
			}
		}
		s.recheckApplicant(app, at)
	}
	s.now = at
	return nil
}
