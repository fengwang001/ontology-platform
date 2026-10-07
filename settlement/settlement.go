package settlement

// MemberResult 为单个成员的结算结果，结算后不可变。
type MemberResult struct {
	PersonID string

	// VersionNo 为评分版本号；0 表示没有任何可评分版本（NoValidVersion）。
	VersionNo int

	// SubmittedAt 为评分版本提交时刻。
	SubmittedAt int

	// LateDuration 为“该成员自己的有效截止”口径下的迟交时长。
	LateDuration int

	// PenaltyBPS 为该成员落档的扣分基点；准时为 0。
	PenaltyBPS int

	// OnTime 为该成员口径下评分版本是否准时。
	OnTime bool

	// Invalid 为该成员口径下评分版本是否超过最后一档上限。
	Invalid bool

	// NoValidVersion 表示没有任何可被选为评分版本的版本
	// （从未提交，或全部版本对该成员均无效）。
	NoValidVersion bool
}

// Settle 在作业关闭后对每个提交主体的每个成员结算并冻结全部状态。
//
//   - now < HardClose 报参数非法（未到关闭时刻不能结算）。
//   - 重复结算报 ErrSettled。
//   - 评分版本：显式指定优先；否则默认取准时版本中最新者；
//     允许迟交覆盖（allowLateOverride 在配置中未开启时即默认规则）。
//   - 小组中每人按自己的有效截止（退出成员只看退出边界内版本）
//     分别取档；同一版本各成员扣分比例可以不同。
func (e *Engine) Settle(assignmentID string, now int) (map[string]*MemberResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, err := e.lookupAssignment(assignmentID)
	if err != nil {
		return nil, err
	}
	if now < a.cfg.HardClose {
		return nil, classified(ErrInvalidParam, "cannot settle before hard close")
	}
	if a.settled {
		return nil, ErrSettled
	}
	if err := e.checkClock(now); err != nil {
		return nil, err
	}

	results := map[string]*MemberResult{}
	if a.cfg.IsGroup {
		for _, p := range a.people {
			if p.groupID == "" {
				// 未加入任何小组的学生没有提交主体，按无有效版本结算。
				results[p.id] = &MemberResult{PersonID: p.id, NoValidVersion: true}
				continue
			}
			g := a.groups[p.groupID]
			versions := g.versions
			if !p.inGroup {
				versions = versionsUntil(g.versions, p.leftSubjNo)
			}
			// 入组边界：成员只能看到其在组期间产生的版本（加入只能
			// 发生在首次提交前，但仍可能晚于初始成员的首个版本）。
			for len(versions) > 0 {
				if _, ok := versions[0].memberDeadline[p.id]; ok {
					break
				}
				versions = versions[1:]
			}
			designate := g.designate
			if designate > 0 {
				idx := -1
				for i, v := range versions {
					if v.No == designate {
						idx = i + 1
						break
					}
				}
				designate = idx
			}
			results[p.id] = settleMember(a, p.id, versions, designate)
		}
	} else {
		for _, p := range a.people {
			results[p.id] = settleMember(a, p.id, a.ownVersions[p.id], a.ownDesignate[p.id])
		}
	}

	a.settled = true
	a.settlement = results
	return cloneResults(results), nil
}

func versionsUntil(vs []*Version, no int) []*Version {
	if no <= 0 {
		return nil
	}
	if no >= len(vs) {
		return vs
	}
	return vs[:no]
}

// settleMember 为单个人从其可见版本集合中选评分版本并按其个人
// 有效截止取档。designate 为主体当前显式指定版本号（0=默认规则）。
func settleMember(a *Assignment, personID string, vs []*Version, designate int) *MemberResult {
	res := &MemberResult{PersonID: personID}

	memberDeadline := func(v *Version) int {
		if a.cfg.IsGroup {
			if d, ok := v.memberDeadline[personID]; ok {
				return d
			}
			// 版本生成时该成员尚不在组（理论上不会被结算看到），回退到
			// 小组有效截止快照，保证确定性。
			return v.Deadline
		}
		return v.Deadline
	}

	// 该成员口径下版本是否有效：迟交时长不超过最后一档上限。
	// 小组作业中主体层面 Invalid（仅按小组延期判超档）不能否决该版本
	// 对某成员的有效性——成员可能凭个人延期仍在档位内。
	memberValid := func(v *Version) bool {
		late := lateAgainst(v.At, memberDeadline(v))
		if late <= 0 {
			return true
		}
		_, tooLate := a.cfg.penaltyFor(late)
		return !tooLate
	}

	var chosen *Version
	if designate > 0 && designate <= len(vs) {
		cand := vs[designate-1]
		if memberValid(cand) {
			chosen = cand
		}
	}
	if chosen == nil {
		if a.cfg.AllowLateOverride {
			for i := len(vs) - 1; i >= 0; i-- {
				if memberValid(vs[i]) {
					chosen = vs[i]
					break
				}
			}
		} else {
			// 默认：准时版本中最新者。
			for i := len(vs) - 1; i >= 0; i-- {
				v := vs[i]
				if v.At <= memberDeadline(v) {
					chosen = v
					break
				}
			}
		}
	}

	if chosen == nil {
		res.NoValidVersion = true
		return res
	}

	dl := memberDeadline(chosen)
	late := lateAgainst(chosen.At, dl)
	res.VersionNo = chosen.No
	res.SubmittedAt = chosen.At
	res.LateDuration = late
	if late <= 0 {
		res.OnTime = true
	} else {
		bps, tooLate := a.cfg.penaltyFor(late)
		res.PenaltyBPS = bps
		res.Invalid = tooLate
	}
	return res
}

func lateAgainst(at, deadline int) int {
	if at <= deadline {
		return 0
	}
	return at - deadline
}

func cloneResults(in map[string]*MemberResult) map[string]*MemberResult {
	out := make(map[string]*MemberResult, len(in))
	for k, v := range in {
		cp := *v
		out[k] = &cp
	}
	return out
}

// Settlement 返回已结算作业的不可变结果快照；未结算时返回 ErrNotFound。
func (e *Engine) Settlement(assignmentID string) (map[string]*MemberResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	a, err := e.lookupAssignment(assignmentID)
	if err != nil {
		return nil, err
	}
	if !a.settled {
		return nil, classified(ErrNotFound, "assignment not settled")
	}
	return cloneResults(a.settlement), nil
}
