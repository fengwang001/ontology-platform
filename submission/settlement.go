package submission

import "sort"

// Settlement 单个成员的结算结果。无可用评分版本或评分版本对该成员无效时
// Valid=false 且按全扣（Penalty=1）处理。
type Settlement struct {
	AssignmentID string
	SubjectID    string // 提交主体：个人作业为学生，小组作业为小组
	Member       string
	VersionNo    int     // 评分版本号，0 表示无评分版本
	Late         int     // 该版本的迟交时长（<=0 为准时）
	Penalty      float64 // 扣分比例
	Valid        bool
	Explicit     bool // 评分版本是否来自显式指定
}

// settle 对作业的每个主体的每个成员结算，结果按 (主体, 成员) 排序，可复现。
func (a *Assignment) settle() []Settlement {
	var out []Settlement
	if a.cfg.GroupMode {
		for _, g := range sortedKeys(a.groups) {
			for _, m := range a.membersOf(g) {
				out = append(out, a.settleMember(g, m))
			}
		}
		for _, m := range sortedKeys(a.leavers) {
			rec := a.leavers[m]
			s := Settlement{AssignmentID: a.cfg.ID, SubjectID: rec.GroupID, Member: m}
			if rec.VersionNo > 0 {
				fillFromVersion(&s, a.versions[rec.GroupID][rec.VersionNo-1], m)
			} else {
				s.Valid, s.Penalty = false, 1
			}
			out = append(out, s)
		}
	} else {
		for _, sub := range sortedKeys(a.versions) {
			out = append(out, a.settleMember(sub, sub))
		}
	}
	return out
}

// settleMember 为单个成员选取评分版本并生成结算。
func (a *Assignment) settleMember(subject, member string) Settlement {
	s := Settlement{AssignmentID: a.cfg.ID, SubjectID: subject, Member: member}
	v, explicit := a.pickVersion(subject, member)
	if v == nil {
		s.Valid, s.Penalty = false, 1
		return s
	}
	s.Explicit = explicit
	fillFromVersion(&s, v, member)
	return s
}

// pickVersion 评分版本选取：显式指定优先；否则默认取最新准时版本，
// 配置允许迟交覆盖时取最新有效版本。判定基于提交时刻的快照，逐成员独立。
func (a *Assignment) pickVersion(subject, member string) (*Version, bool) {
	if no, ok := a.explicit[subject]; ok {
		return a.versions[subject][no-1], true
	}
	vers := a.versions[subject]
	for i := len(vers) - 1; i >= 0; i-- {
		j, ok := vers[i].Judgments[member]
		if !ok {
			continue
		}
		if a.cfg.AllowLateOverride {
			if j.Valid {
				return vers[i], false
			}
		} else if j.OnTime {
			return vers[i], false
		}
	}
	return nil, false
}

func fillFromVersion(s *Settlement, v *Version, member string) {
	s.VersionNo = v.No
	j, ok := v.Judgments[member]
	if !ok || !j.Valid {
		s.Valid, s.Penalty = false, 1
		s.Late = j.Late
		return
	}
	s.Valid = true
	s.Late = j.Late
	s.Penalty = j.Penalty
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
