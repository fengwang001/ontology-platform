package traversal

// ObjectID 标识本体平台上的一个对象实例。
type ObjectID string

// LinkTypeID 标识链接类型（关系定义）。链接类型必须先在图上登记。
type LinkTypeID string

// LinkID 标识一条具体链接实例。同一对对象之间允许存在多条平行链接，
// 每条链接拥有各自独立的 LinkID。
type LinkID string

// Direction 表示沿链接的遍历方向。
type Direction int

const (
	// DirOutbound 仅沿 source -> target 方向扩展。
	DirOutbound Direction = 1
	// DirInbound 仅沿 target -> source 方向扩展。
	DirInbound Direction = 2
	// DirBoth 两个方向都允许扩展（会分别产生两个方向的候选）。
	DirBoth Direction = 3
)

func (d Direction) isValid() bool {
	return d == DirOutbound || d == DirInbound || d == DirBoth
}

// Object 是图中的一个对象实例。
type Object struct {
	ID         ObjectID
	Properties map[string]string
}

// Link 是一条具体的链接实例。Source == Target 时为自环链接。
type Link struct {
	ID     LinkID
	Type   LinkTypeID
	Source ObjectID
	Target ObjectID
}

// TraversalRequest 是一次图遍历的输入。
//
// Start 为起始对象；Directions 给出每个链接类型允许的遍历方向
// （同一链接类型在 map 中只出现一次，DirBoth 等价于同时允许出入两个方向）；
// MaxDepth 为允许的最大跳数（起始对象位于第 0 跳），必须为正整数。
type TraversalRequest struct {
	Start      ObjectID
	Directions map[LinkTypeID]Direction
	MaxDepth   int
}

// TerminalStatus 描述一条返回路径的终态分类。
type TerminalStatus string

const (
	// StatusBoundary 正常扩展到图边界（当前节点在允许方向上已无候选链接）后自然终止。
	StatusBoundary TerminalStatus = "boundary"
	// StatusCycle 检测到真实环路而终止：该跳的目标对象位于本条路径自己的祖先序列中。
	StatusCycle TerminalStatus = "cycle"
	// StatusDepthLimit 达到深度上限被截断而终止（该路径本可继续扩展，但被上限截断）。
	StatusDepthLimit TerminalStatus = "depth_limit"
)

// TraversedLink 记录路径中一跳所经过的链接及实际遍历方向。
type TraversedLink struct {
	LinkID    LinkID
	Type      LinkTypeID
	Direction Direction // 相对于链接存储方向（source->target）实际经过的方向
	From      ObjectID
	To        ObjectID
}

// CycleInfo 记录一条真实环路的判定依据。
type CycleInfo struct {
	// RepeatedObject 是被重复到达的祖先对象。
	RepeatedObject ObjectID
	// AncestorIndex 是该对象在本条路径祖先序列中首次出现的下标（从 0 起）。
	AncestorIndex int
	// ClosingLink 是闭合环路的那一跳。
	ClosingLink TraversedLink
	// AncestorSequence 是判定时使用的祖先序列：
	// 即从起点到闭合跳之前所在节点的对象序列（不含被重复到达的目标节点）。
	AncestorSequence []ObjectID
}

// TraversedPath 是一条从起始对象到某个终态的完整路径。
//
// Nodes 长度恰为 Links 长度加 1；当 Status == StatusCycle 时，
// Nodes 的末节点与祖先序列中的某个节点相同（自环时即与首节点相同），
// 闭合跳记录在 Cycle.ClosingLink 中且同样计入 Links。
type TraversedPath struct {
	Nodes  []ObjectID
	Links  []TraversedLink
	Depth  int
	Status TerminalStatus
	Cycle  *CycleInfo
}

// Stats 记录一次遍历中与判定复杂度相关的计数器，用于可复现地验证
// 「祖先核对开销不随总图规模线性增长」这一性质。
type Stats struct {
	// ExpandedNodes 实际被展开（枚举其候选链接）的节点数。
	ExpandedNodes int
	// CandidateEdges 被枚举并逐跳处理的候选链接数（平行链接各自计一次）。
	CandidateEdges int
	// AncestorChecks 祖先序列归属判定次数，应等于 CandidateEdges。
	AncestorChecks int
	// AncestorProbes 归属判定内部的比较探测次数。
	// 基于每条路径独立哈希集合的实现中，每次判定为 O(1)，
	// 因此 AncestorProbes == AncestorChecks，且与路径长度及总图规模均无关。
	AncestorProbes int
}

// TraversalResult 是一次成功遍历的全部结果。
type TraversalResult struct {
	SnapshotVersion uint64
	Paths           []*TraversedPath
	Stats           Stats
}
