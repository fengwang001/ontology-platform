package settlement

// Version 描述一次提交产生的版本。
//
// 版本号 No 在“该作业该提交主体”内从 1 开始单调递增且连续无洞；
// 被拒绝的提交不消耗版本号。At 为提交时刻。Deadline 为提交时刻
// 该主体的有效截止（个人作业=个人有效截止；小组作业=统一截止+
// 当时可见的小组延期，个人延期不进入主体判定，只影响各成员取档）。
// Invalid 为主体层面的有效性：迟交时长超过最后一档上限即为无效版本，
// 仍记录但不能被选为评分版本。
//
// MemberDeadline 仅小组作业使用：版本生成瞬间每个在组成员的个人
// 有效截止快照（小组延期与个人延期合并取最大），供结算时精确复现；
// 退出成员沿用其退出边界内版本的该快照。
type Version struct {
	No       int
	At       int
	Deadline int
	Invalid  bool

	memberDeadline map[string]int
}

// OnTime 报告该版本在主体层面是否准时（提交时刻不晚于有效截止）。
func (v *Version) OnTime() bool { return v.At <= v.Deadline }

// Late 返回主体层面迟交时长（准时为 0）。
func (v *Version) Late() int {
	if v.At <= v.Deadline {
		return 0
	}
	return v.At - v.Deadline
}

// Submit 由成员 personID 代表提交主体 subjectID 提交一个版本。
//
//   - 个人作业：subjectID 必须等于 personID（该学生本人主体）。
//   - 小组作业：subjectID 为小组 ID，personID 必须是当前在组成员。
//
// 提交时刻晚于硬性关闭时刻一律拒绝（ErrAfterHardClose）；恰等于
// HardClose 仍然接受。迟交超过最后一档上限时版本记录为 Invalid。
func (e *Engine) Submit(assignmentID, subjectID, personID string, now int) (*Version, error) {
	if subjectID == "" || personID == "" {
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
	if a.settled {
		return nil, ErrSettled
	}
	p, ok := a.people[personID]
	if !ok {
		return nil, classified(ErrNotFound, "person not found: "+personID)
	}

	var versions *[]*Version
	deadline := a.cfg.Deadline
	var memberDeadline map[string]int

	if a.cfg.IsGroup {
		if _, ok := a.groups[subjectID]; !ok {
			return nil, classified(ErrNotFound, "group not found: "+subjectID)
		}
	} else {
		if subjectID != personID {
			return nil, classified(ErrNotFound, "individual subject must be the submitter")
		}
	}

	// 优先级：超过硬性关闭 > 状态不允许，关闭检查先行。
	if now > a.cfg.HardClose {
		return nil, ErrAfterHardClose
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}

	if a.cfg.IsGroup {
		g := a.groups[subjectID]
		if !p.inGroup || p.groupID != subjectID || !g.members[personID] {
			return nil, classified(ErrStateNotAllowed, "person is not a member of the group")
		}
		versions = &g.versions
		groupExtra := a.groupExt[g.id].EffectiveAt(now)
		deadline = a.cfg.Deadline + groupExtra
		memberDeadline = map[string]int{}
		for mid := range g.members {
			mp := a.people[mid]
			personalExtra := mp.personal.EffectiveAt(now)
			extra := groupExtra
			if personalExtra > extra {
				extra = personalExtra
			}
			memberDeadline[mid] = a.cfg.Deadline + extra
		}
	} else {
		vs := a.ownVersions[personID]
		versions = &vs
		deadline = a.cfg.Deadline + p.personal.EffectiveAt(now)
	}

	no := len(*versions) + 1
	v := &Version{
		No:             no,
		At:             now,
		Deadline:       deadline,
		memberDeadline: memberDeadline,
	}
	if late := v.Late(); late > 0 {
		if _, tooLate := a.cfg.penaltyFor(late); tooLate {
			v.Invalid = true
		}
	}
	*versions = append(*versions, v)
	if !a.cfg.IsGroup {
		a.ownVersions[personID] = *versions
	}
	return v, nil
}

// Designate 在作业关闭前显式指定某有效版本为评分版本。显式指定优先
// 于默认选取规则。指定不存在的版本报 ErrNotFound；指定无效版本报
// ErrInvalidVersion（可区分）。小组作业中须由在组成员发起，指定对
// 全体成员（含已退出成员的历史版本选择口径不变）生效。
func (e *Engine) Designate(assignmentID, subjectID, personID string, now, versionNo int) error {
	if subjectID == "" || personID == "" || versionNo <= 0 {
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
	if a.settled {
		return ErrSettled
	}
	p, ok := a.people[personID]
	if !ok {
		return classified(ErrNotFound, "person not found: "+personID)
	}

	var versions []*Version
	if a.cfg.IsGroup {
		if _, ok := a.groups[subjectID]; !ok {
			return classified(ErrNotFound, "group not found: "+subjectID)
		}
	} else {
		if subjectID != personID {
			return classified(ErrNotFound, "individual subject must be the requester")
		}
	}
	// 优先级：超过硬性关闭 > 不存在的版本 > 状态不允许 > 指定无效版本。
	if now > a.cfg.HardClose {
		return ErrAfterHardClose
	}
	if a.cfg.IsGroup {
		g := a.groups[subjectID]
		if !p.inGroup || p.groupID != subjectID || !g.members[personID] {
			return classified(ErrStateNotAllowed, "person is not a member of the group")
		}
		versions = g.versions
	} else {
		versions = a.ownVersions[personID]
	}
	if versionNo > len(versions) {
		return classified(ErrNotFound, "version not found")
	}
	v := versions[versionNo-1]
	if v.Invalid {
		return ErrInvalidVersion
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if a.cfg.IsGroup {
		a.groups[subjectID].designate = versionNo
	} else {
		a.ownDesignate[personID] = versionNo
	}
	return nil
}
