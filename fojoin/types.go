// Package fojoin 提供全外连接结果的增量维护器。
//
// 左右两侧行集合按同一键做全外连接，维护器只消费变更日志
// （插入 / 删除），增量地产生结果侧的加减日志，使下游按序
// 应用即可得到与批量重算一致的物化视图。
package fojoin

// Side 标识变更来自连接的哪一侧。
type Side int

const (
	Left Side = iota
	Right
)

func (s Side) String() string {
	if s == Left {
		return "left"
	}
	return "right"
}

// Op 标识变更动作。
type Op int

const (
	Insert Op = iota
	Delete
)

func (o Op) String() string {
	if o == Insert {
		return "insert"
	}
	return "delete"
}

// Change 是一条输入变更：某侧某键上某行标识的插入或删除。
type Change struct {
	Side  Side
	Op    Op
	Key   string
	RowID string
}

// Row 是连接结果中的一行。左行撤回（或左缺）时空位在右侧，
// 右行撤回（或右缺）时空位在左侧，空位以 nil 表示。
type Row struct {
	Key     string
	LeftID  *string // 非空表示真实左行，nil 表示左侧空位
	RightID *string // 非空表示真实右行，nil 表示右侧空位
}

// IsPair 报告该行是否为两侧真实行的配对行。
func (r Row) IsPair() bool { return r.LeftID != nil && r.RightID != nil }

// LogEntry 是输出日志的一条：对某个结果行的加（Add=true）或减（Add=false）。
type LogEntry struct {
	Add bool
	Row Row
}

// Pair 构造两侧均为真实行的配对行。
func Pair(key, leftID, rightID string) Row {
	l, r := leftID, rightID
	return Row{Key: key, LeftID: &l, RightID: &r}
}

// LeftOnly 构造右侧为空位的补足行（右缺或右行被撤回时的形态）。
func LeftOnly(key, leftID string) Row {
	l := leftID
	return Row{Key: key, LeftID: &l}
}

// RightOnly 构造左侧为空位的补足行（左缺或左行被撤回时的形态）。
func RightOnly(key, rightID string) Row {
	r := rightID
	return Row{Key: key, RightID: &r}
}

// equalRow 逐字段比较两行（空位语义：nil 与 nil 相等）。
func equalRow(a, b Row) bool {
	if a.Key != b.Key {
		return false
	}
	eqPtr := func(x, y *string) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return *x == *y
	}
	return eqPtr(a.LeftID, b.LeftID) && eqPtr(a.RightID, b.RightID)
}
