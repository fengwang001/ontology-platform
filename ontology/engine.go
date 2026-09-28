// Package ontology 维护带两个分组维度行流的三层（明细组 / 第一维小计 / 总计）
// 增量计数与求和，并输出可被下游按序重放的变更日志。
package ontology

import "errors"

// Dim 是一个分组维度取值。Null 为 true 表示空值——空值是真实取值，
// 参与分组（组键 (Null=true) 与任何非空键都不同），不表示汇总占位。
type Dim struct {
	Value string
	Null  bool
}

// Op 标识一条增量的类型。
type Op int

const (
	// OpUpsert 按行 ID 插入新行或替换已存在行的取值。
	OpUpsert Op = iota
	// OpDelete 按行 ID 删除一条当前确实存在的行。
	OpDelete
)

// Increment 是一次提交的输入：一次提交恰好一条增量。
type Increment struct {
	RowID string
	Op    Op
	// Value 仅在 OpUpsert 时使用。
	Value int64
	D1    Dim
	D2    Dim
}

// 变更类型。
const (
ChangeUpsert  = "upsert"  // 正向或替换变更
ChangeDelete  = "delete"  // 普通负向变更：组计数仍然大于零
ChangeRetract = "retract" // 撤回：组计数归零、组被删除
)

// 层级编号。
const (
	LayerDetail   = 1 // 明细组：(D1, D2)
	LayerSubtotal = 2 // 第一维小计：D1
	LayerGrand    = 3 // 总计
)

// Change 是变更日志中的一条记录。
// 每次提交依次产生三层各一条（明细 -> 小计 -> 总计）。
//
// 对 upsert/delete，CountDelta/SumDelta 是带符号的增量（插入为正，
// 替换为“新-旧”的差值，删除为负）。
// 对 retract，CountDelta 与 SumDelta 均为 0，CountMag/SumMag 给出
// 被撤回组在撤回前的计数与求和（求和可以为 0；计数归零即删除）。
type Change struct {
	Seq        int64
	Layer      int
	Kind       string
	D1         Dim
	D2         Dim
	CountDelta int64
	SumDelta   int64
	CountMag   int64
	SumMag     int64
}

// 互不相同、可区分的拒绝原因。
var (
ErrEmptyRowID          = errors.New("ontology: row id is empty")
ErrUnknownOp           = errors.New("ontology: unknown increment op")
ErrRowNotFound         = errors.New("ontology: cannot delete row that does not exist")
ErrTooManyDetailGroups = errors.New("ontology: detail group count exceeds limit")
)

// GroupView 是一个现存组的只读视图（计数为零的组已删除，不会出现）。
type GroupView struct {
	D1    Dim
	D2    Dim
	Count int64
	Sum   int64
}

// Engine 是三层增量维护引擎（占位骨架）。
type Engine struct {
	_ int
}

// NewEngine 创建引擎，maxDetailGroups 为明细组数量上限（<=0 表示不限制）。
func NewEngine(maxDetailGroups int) *Engine {
	return &Engine{}
}

// Commit 原子地校验并应用一条增量，成功后追加 3 条变更日志。
func (e *Engine) Commit(inc Increment) ([]Change, error) {
	return nil, nil
}

// View 返回当前三层状态的只读快照与已发布日志序号。
func (e *Engine) View() (details []GroupView, subtotals []GroupView, total *GroupView, seq int64) {
	return nil, nil, nil, 0
}

// Log 返回已发布变更日志的完整副本。
func (e *Engine) Log() []Change {
	return nil
}
