package ontology

// LinkTypeID 标识链接类型。
type LinkTypeID string

// Object 是本体图中的节点。
type Object struct {
	ID   string
	Type string
}

// LinkType 描述一类有向链接：方向固定为源 -> 目标，并携带遍历代价。
type LinkType struct {
	ID       LinkTypeID
	Cost     int
	Reversed bool // 预留：当前模型仅支持正向（源->目标）遍历
}

// Link 是两个对象之间的一条有向链接实例。
type Link struct {
	Type   LinkTypeID
	Source string
	Target string
}

// Actor 表示一次调用的调用方。
type Actor struct {
	ID string
}

// Truncation 是三态截断标记。
type Truncation int

const (
	// TruncComplete：已穷尽深度与扇出限制下的全部对象且已分页返回完。
	TruncComplete Truncation = iota
	// TruncFanout：存在对象因单节点扇出上限被排除（优先级高于深度）。
	TruncFanout
	// TruncDepth：存在对象仅因深度上限而未继续展开。
	TruncDepth
)

func (t Truncation) String() string {
	switch t {
	case TruncComplete:
		return "complete"
	case TruncFanout:
		return "fanout_truncated"
	case TruncDepth:
		return "depth_truncated"
	default:
		return "unknown"
	}
}

// Metrics 是单次请求实际访问规模的内部度量（不对调用者暴露语义保证）。
type Metrics struct {
	ObjectsLoaded int
	LinksScanned  int
	ACLChecks     int
}

// PermissionModel 定义存在性与遍历两种权限。
type PermissionModel interface {
	// CanSee：调用者是否对对象拥有存在性权限。
	CanSee(snap Snapshot, actor Actor, objectID string) bool
	// CanTraverse：调用者是否有权遍历一条链接。
	CanTraverse(snap Snapshot, actor Actor, link Link) bool
}
