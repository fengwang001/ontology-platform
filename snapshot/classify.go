package snapshot

// Side 表示一条写入相对快照边界的归属。
type Side int

const (
	// SideSnapshot 边界之前（含边界本身）。
	SideSnapshot Side = iota
	// SideIncrement 边界之后。
	SideIncrement
)

func (s Side) String() string {
	if s == SideSnapshot {
		return "snapshot"
	}
	return "increment"
}

// OpCounter 记录归属判定付出的基本操作次数，用于复核
// “判定工作量不随数据总规模增长”这一性质。
type OpCounter struct {
	Comparisons int
}

// Classifier 负责增量归属判定。判定是一条纯函数：仅比较写入的
// LSN 与边界 N，一次整数比较完成，与已处理记录总量无关。
type Classifier struct {
	boundary LSN
	Ops      *OpCounter // 可选；非空时统计比较次数
}

// NewClassifier 构造针对边界 boundary 的判定器。
func NewClassifier(boundary LSN, ops *OpCounter) *Classifier {
	return &Classifier{boundary: boundary, Ops: ops}
}

// Classify 判定 lsn 的归属。固定规则（R1）：LSN <= N 归快照侧，
// 否则归增量侧。写入与边界“恰好重合”（LSN == N）时恒判给快照侧，
// 判定结果与判定发生的实际时刻无关。
func (c *Classifier) Classify(lsn LSN) Side {
	if c.Ops != nil {
		c.Ops.Comparisons++
	}
	if lsn <= c.boundary {
		return SideSnapshot
	}
	return SideIncrement
}

// Boundary 返回判定器所依据的边界。
func (c *Classifier) Boundary() LSN { return c.boundary }
