package submission

import (
	"fmt"
	"sync"
)

// System 结算引擎门面。所有操作可并发调用，内部以互斥锁串行化，
// 结果等价于某个串行顺序。时钟只随成功操作推进；被拒绝的操作不改变任何状态。
// 每个操作按固定优先级检查：参数非法 > 时钟回退 > 不存在 > 已结算 >
// 已超过硬性关闭 > 状态不允许 > 延期超出硬性关闭 > 指定的版本无效。
type System struct {
	mu          sync.Mutex
	now         int
	assignments map[string]*Assignment
	extensions  map[string]*Extension
	extSeq      int
}

func NewSystem() *System {
	return &System{
		assignments: map[string]*Assignment{},
		extensions:  map[string]*Extension{},
	}
}

// Now 返回当前时钟（最近一次成功操作的时刻）。
func (s *System) Now() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now
}

func (s *System) checkClock(op string, at int) *Error {
	if at < 0 {
		return newErr(op, ErrInvalidArgument, "negative time %d", at)
	}
	if at < s.now {
		return newErr(op, ErrClockRegression, "at=%d < now=%d", at, s.now)
	}
	return nil
}

func (s *System) getAssignment(op, id string) (*Assignment, *Error) {
	a, ok := s.assignments[id]
	if !ok {
		return nil, newErr(op, ErrNotFound, "assignment %q", id)
	}
	return a, nil
}

// CreateAssignment 创建作业。档位须按时长上限与扣分比例同时严格递增。
func (s *System) CreateAssignment(at int, cfg AssignmentConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "CreateAssignment"
	if cfg.ID == "" || cfg.Deadline < 0 || cfg.HardClose < cfg.Deadline {
		return newErr(op, ErrInvalidArgument, "bad config %+v", cfg)
	}
	for i, t := range cfg.Tiers {
		if t.MaxLate < 0 || t.Penalty < 0 || t.Penalty > 1 {
			return newErr(op, ErrInvalidArgument, "bad tier %+v", t)
		}
		if i > 0 && (t.MaxLate <= cfg.Tiers[i-1].MaxLate || t.Penalty <= cfg.Tiers[i-1].Penalty) {
			return newErr(op, ErrInvalidArgument, "tiers not strictly increasing")
		}
	}
	if _, dup := s.assignments[cfg.ID]; dup {
		return newErr(op, ErrInvalidArgument, "duplicate assignment %q", cfg.ID)
	}
	if err := s.checkClock(op, at); err != nil {
		return err
	}
	s.assignments[cfg.ID] = newAssignment(cfg)
	s.now = at
	return nil
}

// Submit 提交一个版本，返回版本号。失败不消耗版本号。
func (s *System) Submit(at int, assignmentID, studentID, payload string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "Submit"
	if assignmentID == "" || studentID == "" {
		return 0, newErr(op, ErrInvalidArgument, "empty id")
	}
	if err := s.checkClock(op, at); err != nil {
		return 0, err
	}
	a, err := s.getAssignment(op, assignmentID)
	if err != nil {
		return 0, err
	}
	if a.settled {
		return 0, newErr(op, ErrAlreadySettled, "assignment %q", assignmentID)
	}
	if at > a.cfg.HardClose {
		return 0, newErr(op, ErrHardClosed, "at=%d > hardClose=%d", at, a.cfg.HardClose)
	}
	subject := studentID
	members := []string{studentID}
	if a.cfg.GroupMode {
		g, ok := a.studentGroup[studentID]
		if !ok {
			return 0, newErr(op, ErrStateNotAllowed, "student %q not in any group", studentID)
		}
		subject = g
		members = a.membersOf(g)
	}
	v := &Version{
		No:          len(a.versions[subject]) + 1,
		SubjectID:   subject,
		SubmittedBy: studentID,
		At:          at,
		Payload:     payload,
		Judgments:   map[string]Judgment{},
	}
	for _, m := range members {
		v.Judgments[m] = judge(a.effectiveDeadline(m), at, a.cfg.Tiers)
	}
	a.versions[subject] = append(a.versions[subject], v)
	a.firstSubmit = true
	s.now = at
	return v.No, nil
}

// GrantPersonalExtension 授予个人延期，返回延期 ID。
func (s *System) GrantPersonalExtension(at int, assignmentID, studentID string, duration int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "GrantPersonalExtension"
	if assignmentID == "" || studentID == "" || duration <= 0 {
		return "", newErr(op, ErrInvalidArgument, "bad args")
	}
	if err := s.checkClock(op, at); err != nil {
		return "", err
	}
	a, err := s.getAssignment(op, assignmentID)
	if err != nil {
		return "", err
	}
	if a.cfg.GroupMode {
		if _, ok := a.studentGroup[studentID]; !ok {
			return "", newErr(op, ErrNotFound, "subject %q", studentID)
		}
	}
	if a.settled {
		return "", newErr(op, ErrAlreadySettled, "assignment %q", assignmentID)
	}
	if at > a.cfg.HardClose {
		return "", newErr(op, ErrHardClosed, "at=%d > hardClose=%d", at, a.cfg.HardClose)
	}
	if a.cfg.Deadline+duration > a.cfg.HardClose {
		return "", newErr(op, ErrExtensionExceedsHardClose,
			"deadline=%d + duration=%d > hardClose=%d", a.cfg.Deadline, duration, a.cfg.HardClose)
	}
	ext := s.newExtension(a, duration, at)
	ext.StudentID = studentID
	a.grantPersonal(ext)
	s.now = at
	return ext.ID, nil
}

// GrantGroupExtension 授予小组延期，对全体成员生效，返回延期 ID。
func (s *System) GrantGroupExtension(at int, assignmentID, groupID string, duration int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "GrantGroupExtension"
	if assignmentID == "" || groupID == "" || duration <= 0 {
		return "", newErr(op, ErrInvalidArgument, "bad args")
	}
	if err := s.checkClock(op, at); err != nil {
		return "", err
	}
	a, err := s.getAssignment(op, assignmentID)
	if err != nil {
		return "", err
	}
	if !a.cfg.GroupMode {
		return "", newErr(op, ErrInvalidArgument, "assignment %q is not a group assignment", assignmentID)
	}
	if _, ok := a.groups[groupID]; !ok {
		return "", newErr(op, ErrNotFound, "group %q", groupID)
	}
	if a.settled {
		return "", newErr(op, ErrAlreadySettled, "assignment %q", assignmentID)
	}
	if at > a.cfg.HardClose {
		return "", newErr(op, ErrHardClosed, "at=%d > hardClose=%d", at, a.cfg.HardClose)
	}
	if a.cfg.Deadline+duration > a.cfg.HardClose {
		return "", newErr(op, ErrExtensionExceedsHardClose,
			"deadline=%d + duration=%d > hardClose=%d", a.cfg.Deadline, duration, a.cfg.HardClose)
	}
	ext := s.newExtension(a, duration, at)
	ext.GroupID = groupID
	a.grantGroup(ext)
	s.now = at
	return ext.ID, nil
}

func (s *System) newExtension(a *Assignment, duration, at int) *Extension {
	s.extSeq++
	ext := &Extension{
		ID:           fmt.Sprintf("ext-%d", s.extSeq),
		AssignmentID: a.cfg.ID,
		Duration:     duration,
		GrantedAt:    at,
	}
	s.extensions[ext.ID] = ext
	return ext
}

// RevokeExtension 撤销延期，只影响此后的版本。
func (s *System) RevokeExtension(at int, extID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "RevokeExtension"
	if extID == "" {
		return newErr(op, ErrInvalidArgument, "empty id")
	}
	if err := s.checkClock(op, at); err != nil {
		return err
	}
	ext, ok := s.extensions[extID]
	if !ok {
		return newErr(op, ErrNotFound, "extension %q", extID)
	}
	a := s.assignments[ext.AssignmentID]
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q", ext.AssignmentID)
	}
	if at > a.cfg.HardClose {
		return newErr(op, ErrHardClosed, "at=%d > hardClose=%d", at, a.cfg.HardClose)
	}
	if ext.Revoked {
		return newErr(op, ErrStateNotAllowed, "extension %q already revoked", extID)
	}
	a.revoke(ext)
	s.now = at
	return nil
}

// JoinGroup 加入小组，须在作业的首次提交之前。
func (s *System) JoinGroup(at int, assignmentID, groupID, studentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "JoinGroup"
	if assignmentID == "" || groupID == "" || studentID == "" {
		return newErr(op, ErrInvalidArgument, "empty id")
	}
	if err := s.checkClock(op, at); err != nil {
		return err
	}
	a, err := s.getAssignment(op, assignmentID)
	if err != nil {
		return err
	}
	if !a.cfg.GroupMode {
		return newErr(op, ErrInvalidArgument, "assignment %q is not a group assignment", assignmentID)
	}
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q", assignmentID)
	}
	if at > a.cfg.HardClose {
		return newErr(op, ErrHardClosed, "at=%d > hardClose=%d", at, a.cfg.HardClose)
	}
	if a.firstSubmit {
		return newErr(op, ErrStateNotAllowed, "join after first submission")
	}
	if _, ok := a.studentGroup[studentID]; ok {
		return newErr(op, ErrStateNotAllowed, "student %q already in a group", studentID)
	}
	a.joinGroup(groupID, studentID)
	s.now = at
	return nil
}

// LeaveGroup 退出小组：其个人结算以退出时刻小组最新版本为准。
func (s *System) LeaveGroup(at int, assignmentID, studentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "LeaveGroup"
	if assignmentID == "" || studentID == "" {
		return newErr(op, ErrInvalidArgument, "empty id")
	}
	if err := s.checkClock(op, at); err != nil {
		return err
	}
	a, err := s.getAssignment(op, assignmentID)
	if err != nil {
		return err
	}
	if !a.cfg.GroupMode {
		return newErr(op, ErrInvalidArgument, "assignment %q is not a group assignment", assignmentID)
	}
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q", assignmentID)
	}
	if at > a.cfg.HardClose {
		return newErr(op, ErrHardClosed, "at=%d > hardClose=%d", at, a.cfg.HardClose)
	}
	if _, ok := a.studentGroup[studentID]; !ok {
		return newErr(op, ErrStateNotAllowed, "student %q not in any group", studentID)
	}
	a.leaveGroup(studentID)
	s.now = at
	return nil
}

// SelectVersion 显式指定评分版本，优先于默认规则；小组作业中对全体成员生效。
func (s *System) SelectVersion(at int, assignmentID, studentID string, versionNo int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "SelectVersion"
	if assignmentID == "" || studentID == "" || versionNo <= 0 {
		return newErr(op, ErrInvalidArgument, "bad args")
	}
	if err := s.checkClock(op, at); err != nil {
		return err
	}
	a, err := s.getAssignment(op, assignmentID)
	if err != nil {
		return err
	}
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q", assignmentID)
	}
	if at > a.cfg.HardClose {
		return newErr(op, ErrHardClosed, "at=%d > hardClose=%d", at, a.cfg.HardClose)
	}
	subject := studentID
	if a.cfg.GroupMode {
		g, ok := a.studentGroup[studentID]
		if !ok {
			return newErr(op, ErrStateNotAllowed, "student %q not in any group", studentID)
		}
		subject = g
	}
	vers := a.versions[subject]
	if versionNo > len(vers) {
		return newErr(op, ErrNotFound, "version %d of subject %q", versionNo, subject)
	}
	j, ok := vers[versionNo-1].Judgments[studentID]
	if !ok || !j.Valid {
		return newErr(op, ErrVersionInvalid, "version %d invalid for %q", versionNo, studentID)
	}
	a.explicit[subject] = versionNo
	s.now = at
	return nil
}

// Settle 作业关闭后结算，此后该作业全部状态冻结；重复结算报已结算。
func (s *System) Settle(at int, assignmentID string) ([]Settlement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	const op = "Settle"
	if assignmentID == "" {
		return nil, newErr(op, ErrInvalidArgument, "empty id")
	}
	if err := s.checkClock(op, at); err != nil {
		return nil, err
	}
	a, err := s.getAssignment(op, assignmentID)
	if err != nil {
		return nil, err
	}
	if a.settled {
		return nil, newErr(op, ErrAlreadySettled, "assignment %q", assignmentID)
	}
	if at <= a.cfg.HardClose {
		return nil, newErr(op, ErrStateNotAllowed, "assignment %q not closed yet", assignmentID)
	}
	a.settlements = a.settle()
	a.settled = true
	s.now = at
	out := make([]Settlement, len(a.settlements))
	copy(out, a.settlements)
	return out, nil
}

// Versions 返回某提交主体的版本序列（只读副本）。
func (s *System) Versions(assignmentID, subjectID string) []*Version {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.assignments[assignmentID]
	if !ok {
		return nil
	}
	out := make([]*Version, len(a.versions[subjectID]))
	copy(out, a.versions[subjectID])
	return out
}

// Settlements 返回已结算作业的结算结果。
func (s *System) Settlements(assignmentID string) ([]Settlement, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.assignments[assignmentID]
	if !ok || !a.settled {
		return nil, false
	}
	out := make([]Settlement, len(a.settlements))
	copy(out, a.settlements)
	return out, true
}

// EffectiveDeadline 返回某成员当前可见的有效截止时刻。
func (s *System) EffectiveDeadline(assignmentID, studentID string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.assignments[assignmentID]
	if !ok {
		return 0, false
	}
	return a.effectiveDeadline(studentID), true
}
