// Package ontology 实现作业提交截止、延期与迟交扣分结算引擎。
//
// 时间用单调递增的整数时刻表示；所有变更操作携带时刻，被拒绝的操作不推进时钟。
// 全部操作由一把互斥锁串行化，并发调用等价于某个串行顺序。
// 每个变更操作按固定优先级检查拒绝原因：
// 参数非法 > 时钟回退 > 不存在 > 已结算 > 已关闭 > 状态不允许 > 延期超界 > 版本无效。
package ontology

import "sync"

// Engine 是结算引擎，管理全部作业。请用 NewEngine 构造。
type Engine struct {
	mu          sync.Mutex
	clock       int64
	assignments map[string]*assignment
}

func NewEngine() *Engine {
	return &Engine{assignments: map[string]*assignment{}}
}

// checkClock 校验时钟不回退；调用方须已持锁。
func (e *Engine) checkClock(op string, now int64) error {
	if now < e.clock {
		return newErr(op, ErrClockRegression, "now=%d before engine clock %d", now, e.clock)
	}
	return nil
}

func (e *Engine) lookupAssignment(op, assignmentID string) (*assignment, error) {
	a, ok := e.assignments[assignmentID]
	if !ok {
		return nil, newErr(op, ErrNotFound, "assignment %q not found", assignmentID)
	}
	return a, nil
}

// CreateAssignment 创建作业。配置非法返回 ErrInvalidArgument，ID 已存在返回 ErrStateNotAllowed。
func (e *Engine) CreateAssignment(cfg AssignmentConfig, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "CreateAssignment"
	if err := validateConfig(op, cfg); err != nil {
		return err
	}
	if now < 0 {
		return newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	if _, ok := e.assignments[cfg.ID]; ok {
		return newErr(op, ErrStateNotAllowed, "assignment %q already exists", cfg.ID)
	}
	e.assignments[cfg.ID] = newAssignment(cfg)
	e.clock = now
	return nil
}

// Submit 提交一个版本，返回版本号（在该作业该主体内从 1 开始连续递增）。
// 个人作业要求 subjectID == memberID；小组作业要求 memberID 是小组当前成员。
func (e *Engine) Submit(assignmentID, subjectID, memberID string, now int64) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "Submit"
	if assignmentID == "" || subjectID == "" || memberID == "" {
		return 0, newErr(op, ErrInvalidArgument, "empty id")
	}
	if now < 0 {
		return 0, newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if err := e.checkClock(op, now); err != nil {
		return 0, err
	}
	a, err := e.lookupAssignment(op, assignmentID)
	if err != nil {
		return 0, err
	}
	if a.cfg.GroupWork {
		if _, ok := a.groups[subjectID]; !ok {
			return 0, newErr(op, ErrNotFound, "group %q not found", subjectID)
		}
	}
	if a.settled {
		return 0, newErr(op, ErrAlreadySettled, "assignment %q settled", assignmentID)
	}
	if now > a.cfg.HardClose {
		return 0, newErr(op, ErrClosed, "now=%d past hard close %d", now, a.cfg.HardClose)
	}
	var members []string
	if a.cfg.GroupWork {
		g := a.groups[subjectID]
		if !g.members[memberID] {
			return 0, newErr(op, ErrStateNotAllowed, "member %q not in group %q", memberID, subjectID)
		}
		for m := range g.members {
			members = append(members, m)
		}
	} else {
		if subjectID != memberID {
			return 0, newErr(op, ErrStateNotAllowed, "cannot submit for %q as %q", subjectID, memberID)
		}
		members = []string{memberID}
	}
	sub := a.subjects[subjectID]
	if sub == nil {
		sub = newSubject(subjectID)
		a.subjects[subjectID] = sub
	}
	// 主体级判定：小组主体只看小组延期，个人主体只看个人延期。
	var subjEff int64
	if a.cfg.GroupWork {
		subjEff = a.cfg.Deadline + a.groupMax(subjectID)
	} else {
		subjEff = a.cfg.Deadline + a.personalMax(subjectID)
	}
	v := &version{
		number: len(sub.versions) + 1,
		time:   now,
		// submitter 记录提交者，小组内任一成员均可提交。
		submitter: memberID,
		onTime:    now <= subjEff,
		snaps:     map[string]memberSnap{},
	}
	if late := now - subjEff; late > 0 {
		_, ok := findTier(a.cfg.Tiers, late)
		v.invalid = !ok
	}
	// 成员级快照：按各自有效截止取档，写入后不再变化。
	for _, m := range members {
		eff := a.cfg.Deadline + a.personalMax(m)
		if a.cfg.GroupWork {
			if gm := a.groupMax(subjectID); gm > eff-a.cfg.Deadline {
				eff = a.cfg.Deadline + gm
			}
		}
		snap := memberSnap{effDeadline: eff, late: now - eff}
		if snap.late > 0 {
			if t, ok := findTier(a.cfg.Tiers, snap.late); ok {
				snap.penalty = t.Penalty
			} else {
				snap.invalid = true
			}
		}
		v.snaps[m] = snap
	}
	sub.versions = append(sub.versions, v)
	e.clock = now
	return v.number, nil
}

// GrantExtension 授予延期，返回授权编号。有效截止 = 统一截止 + 各次延期时长的最大值。
// 延期不得使有效截止晚于硬性关闭，否则返回 ErrExtensionExceedsClose。
func (e *Engine) GrantExtension(assignmentID string, kind TargetKind, targetID string, duration int64, now int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "GrantExtension"
	if assignmentID == "" || targetID == "" {
		return 0, newErr(op, ErrInvalidArgument, "empty id")
	}
	if kind != PersonalTarget && kind != GroupTarget {
		return 0, newErr(op, ErrInvalidArgument, "unknown target kind %d", kind)
	}
	if duration < 0 {
		return 0, newErr(op, ErrInvalidArgument, "negative duration %d", duration)
	}
	if now < 0 {
		return 0, newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if err := e.checkClock(op, now); err != nil {
		return 0, err
	}
	a, err := e.lookupAssignment(op, assignmentID)
	if err != nil {
		return 0, err
	}
	if kind == GroupTarget {
		if _, ok := a.groups[targetID]; !ok {
			return 0, newErr(op, ErrNotFound, "group %q not found", targetID)
		}
	}
	if a.settled {
		return 0, newErr(op, ErrAlreadySettled, "assignment %q settled", assignmentID)
	}
	if a.cfg.Deadline+duration > a.cfg.HardClose {
		return 0, newErr(op, ErrExtensionExceedsClose,
			"deadline %d + duration %d exceeds hard close %d", a.cfg.Deadline, duration, a.cfg.HardClose)
	}
	a.nextGrant++
	g := &grant{id: a.nextGrant, kind: kind, target: targetID, duration: duration}
	a.grants[g.id] = g
	if kind == PersonalTarget {
		extSetOf(a.personal, targetID).add(duration)
	} else {
		extSetOf(a.groupExt, targetID).add(duration)
	}
	e.clock = now
	return g.id, nil
}

// RevokeExtension 撤销一次延期授权，只影响此后的版本。
func (e *Engine) RevokeExtension(assignmentID string, grantID int64, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "RevokeExtension"
	if assignmentID == "" || grantID <= 0 {
		return newErr(op, ErrInvalidArgument, "empty assignment or non-positive grant id")
	}
	if now < 0 {
		return newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	a, err := e.lookupAssignment(op, assignmentID)
	if err != nil {
		return err
	}
	g, ok := a.grants[grantID]
	if !ok || g.revoked {
		return newErr(op, ErrNotFound, "grant %d not found", grantID)
	}
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q settled", assignmentID)
	}
	g.revoked = true
	if g.kind == PersonalTarget {
		extSetOf(a.personal, g.target).remove(g.duration)
	} else {
		extSetOf(a.groupExt, g.target).remove(g.duration)
	}
	e.clock = now
	return nil
}

func extSetOf(m map[string]*extSet, id string) *extSet {
	s, ok := m[id]
	if !ok {
		s = newExtSet()
		m[id] = s
	}
	return s
}

// JoinGroup 加入小组（小组不存在时创建）。须在首次提交之前；
// 首次提交之后加入返回 ErrStateNotAllowed。已退出者不得再次加入同一小组。
func (e *Engine) JoinGroup(assignmentID, groupID, memberID string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "JoinGroup"
	if assignmentID == "" || groupID == "" || memberID == "" {
		return newErr(op, ErrInvalidArgument, "empty id")
	}
	if now < 0 {
		return newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	a, err := e.lookupAssignment(op, assignmentID)
	if err != nil {
		return err
	}
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q settled", assignmentID)
	}
	if now > a.cfg.HardClose {
		return newErr(op, ErrClosed, "now=%d past hard close %d", now, a.cfg.HardClose)
	}
	if !a.cfg.GroupWork {
		return newErr(op, ErrStateNotAllowed, "assignment %q is not group work", assignmentID)
	}
	if _, busy := a.memberGroup[memberID]; busy {
		return newErr(op, ErrStateNotAllowed, "member %q already in a group", memberID)
	}
	g, ok := a.groups[groupID]
	if ok {
		if _, left := g.former[memberID]; left {
			return newErr(op, ErrStateNotAllowed, "member %q already left group %q", memberID, groupID)
		}
		if sub := a.subjects[groupID]; sub != nil && len(sub.versions) > 0 {
			return newErr(op, ErrStateNotAllowed, "group %q already submitted", groupID)
		}
	} else {
		g = newGroup(groupID)
		a.groups[groupID] = g
		a.subjects[groupID] = newSubject(groupID)
	}
	g.members[memberID] = true
	a.memberGroup[memberID] = groupID
	e.clock = now
	return nil
}

// LeaveGroup 退出小组。退出后其个人结算以退出时刻小组的最新版本为准。
func (e *Engine) LeaveGroup(assignmentID, groupID, memberID string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "LeaveGroup"
	if assignmentID == "" || groupID == "" || memberID == "" {
		return newErr(op, ErrInvalidArgument, "empty id")
	}
	if now < 0 {
		return newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	a, err := e.lookupAssignment(op, assignmentID)
	if err != nil {
		return err
	}
	g, ok := a.groups[groupID]
	if !ok {
		return newErr(op, ErrNotFound, "group %q not found", groupID)
	}
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q settled", assignmentID)
	}
	if now > a.cfg.HardClose {
		return newErr(op, ErrClosed, "now=%d past hard close %d", now, a.cfg.HardClose)
	}
	if !g.members[memberID] {
		return newErr(op, ErrStateNotAllowed, "member %q not in group %q", memberID, groupID)
	}
	delete(g.members, memberID)
	delete(a.memberGroup, memberID)
	g.former[memberID] = len(a.subjects[groupID].versions)
	e.clock = now
	return nil
}

// DesignateVersion 显式指定评分版本，优先于默认规则；小组中对全体成员生效。
// 指定主体级无效版本返回 ErrVersionInvalid。
func (e *Engine) DesignateVersion(assignmentID, subjectID, memberID string, versionNum int, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "DesignateVersion"
	if assignmentID == "" || subjectID == "" || memberID == "" || versionNum <= 0 {
		return newErr(op, ErrInvalidArgument, "empty id or non-positive version")
	}
	if now < 0 {
		return newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if err := e.checkClock(op, now); err != nil {
		return err
	}
	a, err := e.lookupAssignment(op, assignmentID)
	if err != nil {
		return err
	}
	sub, ok := a.subjects[subjectID]
	if !ok {
		return newErr(op, ErrNotFound, "subject %q not found", subjectID)
	}
	if versionNum > len(sub.versions) {
		return newErr(op, ErrNotFound, "version %d not found", versionNum)
	}
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q settled", assignmentID)
	}
	if now > a.cfg.HardClose {
		return newErr(op, ErrClosed, "now=%d past hard close %d", now, a.cfg.HardClose)
	}
	if a.cfg.GroupWork {
		if !a.groups[subjectID].members[memberID] {
			return newErr(op, ErrStateNotAllowed, "member %q not in group %q", memberID, subjectID)
		}
	} else if subjectID != memberID {
		return newErr(op, ErrStateNotAllowed, "cannot designate for %q as %q", subjectID, memberID)
	}
	if sub.versions[versionNum-1].invalid {
		return newErr(op, ErrVersionInvalid, "version %d is invalid", versionNum)
	}
	sub.designated = versionNum
	e.clock = now
	return nil
}

// Settle 在作业关闭后结算，冻结全部状态。重复结算返回 ErrAlreadySettled。
func (e *Engine) Settle(assignmentID string, now int64) ([]MemberSettlement, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "Settle"
	if assignmentID == "" {
		return nil, newErr(op, ErrInvalidArgument, "empty assignment id")
	}
	if now < 0 {
		return nil, newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if err := e.checkClock(op, now); err != nil {
		return nil, err
	}
	a, err := e.lookupAssignment(op, assignmentID)
	if err != nil {
		return nil, err
	}
	if a.settled {
		return nil, newErr(op, ErrAlreadySettled, "assignment %q settled", assignmentID)
	}
	if now <= a.cfg.HardClose {
		return nil, newErr(op, ErrStateNotAllowed, "now=%d not past hard close %d", now, a.cfg.HardClose)
	}
	a.result = a.computeSettlement()
	a.settled = true
	e.clock = now
	out := make([]MemberSettlement, len(a.result))
	copy(out, a.result)
	return out, nil
}

// EffectiveDeadline 返回成员当前的有效截止时刻：
// 统一截止 + 其当前可见延期（个人延期与所在小组延期）的最大延长时长。
// 查询为只读操作，不推进时钟；开销不随延期总数增长。
func (e *Engine) EffectiveDeadline(assignmentID, memberID string) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	const op = "EffectiveDeadline"
	if assignmentID == "" || memberID == "" {
		return 0, newErr(op, ErrInvalidArgument, "empty id")
	}
	a, err := e.lookupAssignment(op, assignmentID)
	if err != nil {
		return 0, err
	}
	best := a.personalMax(memberID)
	if groupID, ok := a.memberGroup[memberID]; ok {
		if gm := a.groupMax(groupID); gm > best {
			best = gm
		}
	}
	return a.cfg.Deadline + best, nil
}
