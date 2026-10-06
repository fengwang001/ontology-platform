package submission

// Judgment 是提交时刻对某成员的一次性判定快照：
// 之后的延期授予/撤销不回溯已产生的版本。
type Judgment struct {
	EffectiveDeadline int     // 提交时刻该成员的有效截止
	Late              int     // 迟交时长 = 提交时刻 - 有效截止（<=0 为准时）
	Penalty           float64 // 按档位取得的扣分比例，准时为 0
	OnTime            bool
	Valid             bool // 迟交超过最后一档上限时为无效提交
}

// Version 一次提交产生的版本。
type Version struct {
	No          int // 在该作业该提交主体内从 1 开始连续递增
	SubjectID   string
	SubmittedBy string
	At          int
	Payload     string
	Judgments   map[string]Judgment // 成员 -> 提交时刻的判定快照
}

// judge 以提交时刻的有效截止判定迟交时长与扣分档位。
func judge(effectiveDeadline, at int, tiers []Tier) Judgment {
	late := at - effectiveDeadline
	j := Judgment{
		EffectiveDeadline: effectiveDeadline,
		Late:              late,
		OnTime:            late <= 0,
		Valid:             true,
	}
	if j.OnTime {
		return j
	}
	t, ok := lookupTier(tiers, late)
	if !ok {
		j.Valid = false // 超过最后一档上限：仍记录但不得被选为评分版本
		return j
	}
	j.Penalty = t.Penalty
	return j
}
