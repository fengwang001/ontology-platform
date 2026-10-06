package scholarship

import "time"

// reevaluate 使用当前数据从零计算分配，再并入已确认集合：
// 与新结论冲突的已确认奖励原样保留、仍占用其原来源名额（由 allocate 预占）；
// 未冲突的已确认奖励也由同一条从零计算路径产出，因此结果与
// 「变更后数据从零评定 + 强制保留」逐字节相同，且不依赖任何历史评定次数。
func (e *Engine) reevaluate(at time.Time) *Result {
	awards, ranking := e.allocate(at, e.confirmed)
	res := &Result{EvaluatedAt: at, Awards: awards, Ranking: ranking}
	res.Levels = make([]string, len(e.levels))
	for i := range e.levels {
		res.Levels[i] = e.levels[i].ID
	}
	e.result = res
	return res
}

// currentResultLocked 返回最近一次评定结果；未评定时返回 ErrNotEvaluated。
func (e *Engine) currentResultLocked() (*Result, error) {
	if e.result == nil {
		return nil, errf(ErrNotEvaluated, "no evaluation result yet")
	}
	return e.result, nil
}
