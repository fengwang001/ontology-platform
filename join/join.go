// Package join 实现全外连接结果的增量维护器。
//
// 左右两侧行集合按同一键做全外连接，维护器只通过变更日志（Add/Retract
// 条目）增量维护结果：每个键上若左右两侧都有行，则输出两侧行数乘积个
// 配对行；只有一侧有行，则输出该侧行数加另一侧空位补足行；两侧都无则
// 无行。补位行与真实配对行之间的切换只在某侧计数穿越零时发生，且结果
// 与批量重算一致。
package join

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

// Op 标识变更或日志条目的操作类型。
type Op int

const (
	// Insert / Add：插入输入行 / 向视图增加输出行。
	Insert Op = iota
	// Delete / Retract：删除输入行 / 从视图撤回输出行。
	Delete
)

func (op Op) String() string {
	if op == Insert {
		return "add"
	}
	return "retract"
}

// Change 是一条输入变更：向某侧的某个键插入或删除一行。
type Change struct {
	Side Side
	Op   Op
	Key  string
	Row  string
}

// Row 是视图中的一行输出。
//
// 配对行左右两侧均为真实行标识（HasLeft 与 HasRight 均为 true）；
// 补位行只有一侧为真实行标识，另一侧为空位（对应 Has 标志为 false）。
// 左行单独存在时空位在右侧，右行单独存在时空位在左侧。
type Row struct {
	Key      string
	Left     string
	HasLeft  bool
	Right    string
	HasRight bool
}

// Entry 是变更日志中的一条有序条目，下游按 Seq 顺序应用即可得到物化视图。
type Entry struct {
	Seq int
	Op  Op
	Row Row
}

// RejectCode 是可判定的拒绝原因代码，三类非法输入互不相同。
type RejectCode int

const (
	// RejectEmptyKey：键为空。
	RejectEmptyKey RejectCode = iota + 1
	// RejectDuplicateRow：重复插入同一行标识。
	RejectDuplicateRow
	// RejectRowNotFound：删除不存在的行标识。
	RejectRowNotFound
)

func (c RejectCode) String() string {
	switch c {
	case RejectEmptyKey:
		return "empty-key"
	case RejectDuplicateRow:
		return "duplicate-row"
	case RejectRowNotFound:
		return "row-not-found"
	}
	return "unknown"
}

// RejectError 描述一批变更中某一条被拒绝的原因。
// 任一批内任一条被拒，则整批不生效，状态与已输出日志均不改变。
type RejectError struct {
	Code   RejectCode
	Index  int // 批内第几条变更（从 0 开始）
	Change Change
}

func (e *RejectError) Error() string {
	return "join: change rejected (" + e.Code.String() + ")"
}
