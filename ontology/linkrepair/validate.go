package linkrepair

// validator 负责单条记录的结构有效性判定（阶段 1）。
type validator struct {
	knownTypes map[string]struct{}
}

// newValidator 依据已知链接类型集合构建结构判定器。
func newValidator(types []LinkType) *validator {
	known := make(map[string]struct{}, len(types))
	for _, t := range types {
		known[t.ID] = struct{}{}
	}
	return &validator{knownTypes: known}
}

// malformedDetail 返回记录结构损坏的确定性说明；结构有效时返回空串。
//
// 结构损坏包括（按检查顺序给出，保证说明稳定且唯一）：
//  1. 链接类型标识丢失或指向未知类型；
//  2. A 端对象信息丢失；
//  3. B 端对象信息丢失。
//
// 只残留一端信息的"不完整记录"在此被整体判定为不可用，
// 不使用任何默认值或猜测补全缺失端。
func (v *validator) malformedDetail(rec RawRecord) string {
	if rec.LinkTypeID == "" {
		return "missing link type"
	}
	if _, ok := v.knownTypes[rec.LinkTypeID]; !ok {
		return "unknown link type: " + rec.LinkTypeID
	}
	if rec.SourceID == "" {
		return "missing source (end A) object"
	}
	if rec.TargetID == "" {
		return "missing target (end B) object"
	}
	return ""
}
