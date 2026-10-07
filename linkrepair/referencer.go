package linkrepair

// Referencer 负责完整链接的引用可用性核对（裁决优先级第 2 级）。
//
// 结构完好的链接只有在两端对象都属于“同一次恢复中已判定可用”的
// 对象集合时才允许进入后续裁决；任一端不可用则整条舍弃，
// 且该原因与结构损坏、基数冲突、重复三者严格区分。
type Referencer struct {
	available map[ObjectID]bool
}

// NewReferencer 基于本次恢复的可用对象集合构造引用核对器。
func NewReferencer(available map[ObjectID]bool) *Referencer {
	return &Referencer{available: available}
}

// Available 报告对象实例在本次恢复中是否可用。
func (r *Referencer) Available(id ObjectID) bool { return r.available[id] }

// Resolve 校验链接两端对象是否都可用；
// 返回不可用的对象标识（两端都不可用时优先报告 From 端）。
func (r *Referencer) Resolve(link Link) (ObjectID, bool) {
	if !r.available[link.From] {
		return link.From, false
	}
	if !r.available[link.To] {
		return link.To, false
	}
	return "", true
}
