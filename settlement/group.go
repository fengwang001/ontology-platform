package settlement

// 小组归属规则：
//
//   - 小组作业以小组为提交主体，任一成员的提交都是小组的版本。
//   - 成员只能在首次提交之前加入；首次提交之后加入报状态不允许。
//   - 成员可在作业关闭前退出。退出时以当时小组最新版本为其个人
//     结算边界（leftSubjNo）；退出后小组的新版本与其无关。
//   - 退出后不得重新加入任何小组；一个成员同一时刻至多属于一个小组。

// CreateGroup 创建小组并登记初始成员。初始成员不得已在其他组、
// 不得已退出过小组；全部成员须已登记到作业。
func (e *Engine) CreateGroup(assignmentID, groupID string, now int, memberIDs []string) error {
	if groupID == "" || len(memberIDs) == 0 || hasEmpty(memberIDs) || hasDup(memberIDs) {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.rollback(now); err != nil {
		return err
	}
	a, err := e.lookupAssignment(assignmentID)
	if err != nil {
		return err
	}
	if !a.cfg.IsGroup {
		return classified(ErrInvalidParam, "assignment is not a group assignment")
	}
	if a.settled {
		return ErrSettled
	}
	if now > a.cfg.HardClose {
		return ErrAfterHardClose
	}
	if _, ok := a.groups[groupID]; ok {
		return classified(ErrInvalidParam, "group already exists: "+groupID)
	}
	for _, pid := range memberIDs {
		p, ok := a.people[pid]
		if !ok {
			return classified(ErrNotFound, "person not found: "+pid)
		}
		if p.groupID != "" || p.leftAt != 0 {
			return classified(ErrStateNotAllowed, "person unavailable for grouping: "+pid)
		}
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	g := &Group{id: groupID, members: map[string]bool{}}
	for _, pid := range memberIDs {
		g.members[pid] = true
		p := a.people[pid]
		p.groupID = groupID
		p.inGroup = true
	}
	a.groups[groupID] = g
	a.groupExt[groupID] = newExtIndex()
	return nil
}

// Join 在首次提交之前加入小组。小组已有任意版本后加入报状态不允许；
// 已退出过、已在组（含其他组）同样报状态不允许。
func (e *Engine) Join(assignmentID, groupID, personID string, now int) error {
	if groupID == "" || personID == "" {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.rollback(now); err != nil {
		return err
	}
	a, err := e.lookupAssignment(assignmentID)
	if err != nil {
		return err
	}
	if !a.cfg.IsGroup {
		return classified(ErrInvalidParam, "assignment is not a group assignment")
	}
	if a.settled {
		return ErrSettled
	}
	g, ok := a.groups[groupID]
	if !ok {
		return classified(ErrNotFound, "group not found: "+groupID)
	}
	p, ok := a.people[personID]
	if !ok {
		return classified(ErrNotFound, "person not found: "+personID)
	}
	if now > a.cfg.HardClose {
		return ErrAfterHardClose
	}
	if p.groupID != "" || p.leftAt != 0 {
		return classified(ErrStateNotAllowed, "person cannot join: "+personID)
	}
	if len(g.versions) > 0 {
		return classified(ErrStateNotAllowed, "group already has submissions")
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	g.members[personID] = true
	p.groupID = groupID
	p.inGroup = true
	return nil
}

// Leave 在作业关闭前退出小组。返回退出时刻快照：LeftAt 为退出时刻，
// LatestSubjNo 为退出时小组最新版本号（无版本为 0）。非本组成员操作
// 报状态不允许。
func (e *Engine) Leave(assignmentID, groupID, personID string, now int) (*LeaveInfo, error) {
	if groupID == "" || personID == "" {
		return nil, ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.rollback(now); err != nil {
		return nil, err
	}
	a, err := e.lookupAssignment(assignmentID)
	if err != nil {
		return nil, err
	}
	if !a.cfg.IsGroup {
		return nil, classified(ErrInvalidParam, "assignment is not a group assignment")
	}
	if a.settled {
		return nil, ErrSettled
	}
	g, ok := a.groups[groupID]
	if !ok {
		return nil, classified(ErrNotFound, "group not found: "+groupID)
	}
	p, ok := a.people[personID]
	if !ok {
		return nil, classified(ErrNotFound, "person not found: "+personID)
	}
	if now > a.cfg.HardClose {
		return nil, ErrAfterHardClose
	}
	if !p.inGroup || p.groupID != groupID || !g.members[personID] {
		return nil, classified(ErrStateNotAllowed, "not a member of the group")
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}
	latest := 0
	if n := len(g.versions); n > 0 {
		latest = g.versions[n-1].No
	}
	delete(g.members, personID)
	p.inGroup = false
	p.leftAt = now
	p.leftSubjNo = latest
	return &LeaveInfo{LeftAt: now, LatestSubjNo: latest}, nil
}

// LeaveInfo 记录退出时刻的快照信息。
type LeaveInfo struct {
	LeftAt       int
	LatestSubjNo int
}

func hasEmpty(xs []string) bool {
	for _, x := range xs {
		if x == "" {
			return true
		}
	}
	return false
}

func hasDup(xs []string) bool {
	seen := map[string]bool{}
	for _, x := range xs {
		if seen[x] {
			return true
		}
		seen[x] = true
	}
	return false
}
