package linkrepair

// referenceChecker 负责引用可用性核对（阶段 2）。
type referenceChecker struct {
	available map[string]struct{}
}

// newReferenceChecker 依据同一次恢复中可用的对象集合构建核对器。
func newReferenceChecker(available map[string]struct{}) *referenceChecker {
	return &referenceChecker{available: available}
}

// unavailableEnds 返回记录中不可用端的确定性说明；全部可用时返回空串。
// 调用方必须先确认记录结构有效（两端非空）。
//
// 核对依据是同一次恢复过程产出的可用对象集合：凡不在该集合中的对象，
// 一律视为已被判定不可用。只要一端不可用，整条记录即被舍弃，
// 该原因与结构损坏、基数冲突、重复互斥。
func (c *referenceChecker) unavailableEnds(rec RawRecord) string {
	_, sourceOK := c.available[rec.SourceID]
	_, targetOK := c.available[rec.TargetID]
	switch {
	case !sourceOK && !targetOK:
		return "both referenced objects unavailable: " + rec.SourceID + ", " + rec.TargetID
	case !sourceOK:
		return "referenced source object unavailable: " + rec.SourceID
	case !targetOK:
		return "referenced target object unavailable: " + rec.TargetID
	}
	return ""
}
