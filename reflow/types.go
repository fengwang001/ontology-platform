package reflow

// NodeID 标识盒树中的一个节点（含已从树上摘下的节点）。
type NodeID uint64

// ModeKind 为宽/高模式枚举，0 值即 Auto。
type ModeKind uint8

const (
	ModeAuto  ModeKind = 0
	ModeFixed ModeKind = 1
)

// Mode 描述一个维度的取值模式。Value 仅在 Fixed 下使用，必须非负。
type Mode struct {
	Kind  ModeKind
	Value int64
}

// Auto 返回内容决定模式。
func Auto() Mode { return Mode{Kind: ModeAuto} }

// Fixed 返回固定值模式，v 为负将在使用处被判为参数非法。
func Fixed(v int64) Mode { return Mode{Kind: ModeFixed, Value: v} }

// Size 是节点当前（已提交）的实际尺寸。
type Size struct {
	W int64
	H int64
}

// Change 记录一次重排中某节点的新旧尺寸，按重算次序返回。
type Change struct {
	ID  NodeID
	Old Size
	New Size
}

// Stats 为可验证的性能计数，仅统计实际发生的传播/重算步数。
type Stats struct {
	// MarkPropSteps 为属性/结构修改时脏标记沿祖先传播的步进数。
	MarkPropSteps int64
	// RecomputeVisits 为重排中真正重算的节点数（含边界本身）。
	RecomputeVisits int64
	// Commits 为已完成的显式/隐式提交次数。
	Commits int64
}
