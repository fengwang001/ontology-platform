package ontology

// Direction 表示链接类型相对于持有基数约束的对象实例所在的一侧。
type Direction int

const (
	Outgoing Direction = iota
	Incoming
)

// Cardinality 描述一个对象实例在某链接类型（及方向）上
// 允许关联的另一侧实例数量上限。
type Cardinality struct {
	LinkType  string
	Direction Direction
	Max       int
}

// LinkOp 是一次写入请求中的单个链接增删操作。
type LinkOp struct {
	LinkType  string
	Direction Direction
	OtherID   string
	Add       bool
}

// Change 是针对单个目标实例的一次逻辑写入请求。
// BaseVersion 是调用方读取该实例时的基线版本。
type Change struct {
	ObjectID    string
	BaseVersion int64
	Ops         []LinkOp
}

// Snapshot 是目标实例在某一时刻的完整读取结果。
// Counts 以“链接类型|方向”为键给出当前关联数量，
// Links 给出当前实际持有的全部关联（用于完整记录与重放）。
type Snapshot struct {
	ObjectID string
	Version  int64
	Counts   map[string]int
	Links    map[string]map[string]bool
}

// Key 返回一个约束（链接类型 + 方向）在索引中的唯一键。
func (c Cardinality) Key() string { return keyOf(c.LinkType, c.Direction) }

// Key 返回单个链接操作命中的约束键。
func (op LinkOp) Key() string { return keyOf(op.LinkType, op.Direction) }

func keyOf(linkType string, dir Direction) string {
	if dir == Outgoing {
		return "out:" + linkType
	}
	return "in:" + linkType
}

// String 仅用于日志与测试可读性。
func (d Direction) String() string {
	if d == Outgoing {
		return "out"
	}
	return "in"
}

func cloneSnapshot(s *Snapshot) *Snapshot {
	if s == nil {
		return nil
	}
	cp := &Snapshot{
		ObjectID: s.ObjectID,
		Version:  s.Version,
		Counts:   make(map[string]int, len(s.Counts)),
		Links:    make(map[string]map[string]bool, len(s.Links)),
	}
	for k, v := range s.Counts {
		cp.Counts[k] = v
	}
	for k, set := range s.Links {
		cloned := make(map[string]bool, len(set))
		for other := range set {
			cloned[other] = true
		}
		cp.Links[k] = cloned
	}
	return cp
}
