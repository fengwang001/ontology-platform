package scholarship

import "time"

// checkEligibility 判定单个学生在单个等级上的资格。
// 只读取该学生自身记录, 开销与学生总数无关(O(1), 由 Stats.StudentReads 验证)。
// 恰等于下限视为达到; 解除时刻不晚于评定时刻的处分视为已解除。
// 多条不满足时按 处分 > 不及格 > 学分不足 > 平均成绩不足 报告首条。
func checkEligibility(s *Student, lv *LevelConfig, at time.Time) FailReason {
	for _, d := range s.Disciplines {
		if d.LiftedAt.IsZero() || d.LiftedAt.After(at) {
			return ReasonDiscipline
		}
	}
	if s.HasFailRecord {
		return ReasonFailRecord
	}
	if s.Credits < lv.MinCredits {
		return ReasonCredits
	}
	if s.AvgGrade < lv.MinAvg {
		return ReasonAvgGrade
	}
	return ReasonNone
}
