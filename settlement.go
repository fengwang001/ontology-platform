package ontology

import "sort"

// MemberSettlement 是结算结果：每个主体的每个成员各一条。
// Version 为 0 表示无评分版本（此时 Invalid 必为 true）。
type MemberSettlement struct {
	AssignmentID string
	SubjectID    string
	MemberID     string
	Version      int     // 评分版本号
	Late         int64   // 该版本提交时刻 - 该成员有效截止（<=0 为准时）
	Penalty      float64 // 按该成员有效截止取档的扣分比例
	Invalid      bool    // 无评分版本，或该版本对该成员迟交超过最后一档
}

// gradingVersion 选取评分版本号：显式指定优先；
// 否则按配置取全部有效版本最新者或准时版本最新者；无则 0。
func (a *assignment) gradingVersion(sub *subject) int {
	if sub.designated != 0 {
		return sub.designated
	}
	for i := len(sub.versions); i >= 1; i-- {
		v := sub.versions[i-1]
		if a.cfg.AllowLateOverride {
			if !v.invalid {
				return i
			}
		} else if v.onTime {
			return i
		}
	}
	return 0
}

// computeSettlement 对当前全部主体逐成员结算，结果按 (SubjectID, MemberID) 排序。
func (a *assignment) computeSettlement() []MemberSettlement {
	var out []MemberSettlement
	emit := func(sub *subject, memberID string, vnum int) {
		ms := MemberSettlement{
			AssignmentID: a.cfg.ID,
			SubjectID:    sub.id,
			MemberID:     memberID,
		}
		if vnum >= 1 && vnum <= len(sub.versions) {
			if snap, ok := sub.versions[vnum-1].snaps[memberID]; ok {
				ms.Version = vnum
				ms.Late = snap.late
				ms.Penalty = snap.penalty
				ms.Invalid = snap.invalid
			}
		}
		if ms.Version == 0 {
			ms.Invalid = true
		}
		out = append(out, ms)
	}
	for _, sub := range a.subjects {
		g := a.gradingVersion(sub)
		if a.cfg.GroupWork {
			grp := a.groups[sub.id]
			for m := range grp.members {
				emit(sub, m, g)
			}
			for m, leaveVersion := range grp.former {
				emit(sub, m, leaveVersion)
			}
		} else {
			emit(sub, sub.id, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SubjectID != out[j].SubjectID {
			return out[i].SubjectID < out[j].SubjectID
		}
		return out[i].MemberID < out[j].MemberID
	})
	return out
}
