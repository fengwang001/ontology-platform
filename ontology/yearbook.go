package ontology

// yearState 是单个保单年度的累计账（O(1) 状态，无历史列表依赖）。
type yearState struct {
	deductUsed int64 // 本年度已扣免赔
	oopUsed    int64 // 本年度自付累计
}

// yearIndex 返回事故日所属保单年度的序号（自 0 起）。
func yearIndex(spec PolicySpec, day int64) int64 {
	return (day - spec.InceptDay) / spec.YearLen
}

func newYearState() *yearState { return &yearState{} }
