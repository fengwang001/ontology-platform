package scholarship

import "time"

// activeSanction 报告该学生在 at 时刻是否存在生效中的处分。
// 处分的解除时刻不晚于 at（恰等于视为已解除）时不再生效。
func activeSanction(s *Student, at time.Time) bool {
	for i := range s.Sanctions {
		rel := s.Sanctions[i].ReleasedAt
		if rel.IsZero() || rel.After(at) {
			return true
		}
	}
	return false
}

// judge 判定单名学生在给定等级、给定评定时刻下的资格。
// 复杂度仅取决于该学生自身的数据，与学生总数无关。
func judge(s *Student, lv LevelConfig, at time.Time) Eligibility {
	res := Eligibility{StudentID: s.ID, Level: lv.ID}
	// 多条不满足时只报首个，优先级：处分 > 不及格 > 学分不足 > 平均成绩不足。
	switch {
	case activeSanction(s, at):
		res.Reason = ReasonSanction
	case s.HasFail:
		res.Reason = ReasonFail
	case s.Credits < lv.MinCredits: // 恰等于视为达到
		res.Reason = ReasonCredits
	case s.Average < lv.MinAverage:
		res.Reason = ReasonAverage
	default:
		res.Eligible = true
		res.Reason = ReasonEligible
	}
	return res
}
