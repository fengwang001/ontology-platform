// Package sheet 实现多人协同表格的按用户选择性撤销与重做服务。
package sheet

// 取值与时间范围约束。
const (
	MinValue int64 = -1_000_000_000_000_000 // 单元格最小值 -10^15
	MaxValue int64 = 1_000_000_000_000_000  // 单元格最大值 10^15
	MaxNow   int64 = 1_000_000_000_000      // now 最大值 10^12（秒）
	MaxEdits       = 50                     // 单次 Apply 的最大编辑条数
	MinDepth       = 1                      // 历史深度下限
	MaxDepth       = 100                    // 历史深度上限
)

// Code 是操作结果码。
type Code int

const (
	OK               Code = iota // 成功
	ErrInvalidParam              // 参数非法
	ErrClockRollback             // 时钟回退
	ErrProtected                 // 受他人保护
	ErrNoChange                  // 全部编辑无效，无变化
	ErrEmptyStack                // 撤销/重做栈为空
	ErrOverwritten               // 记录已被他人覆盖
)

func (c Code) String() string {
	switch c {
	case OK:
		return "OK"
	case ErrInvalidParam:
		return "InvalidParam"
	case ErrClockRollback:
		return "ClockRollback"
	case ErrProtected:
		return "Protected"
	case ErrNoChange:
		return "NoChange"
	case ErrEmptyStack:
		return "EmptyStack"
	case ErrOverwritten:
		return "Overwritten"
	default:
		return "Unknown"
	}
}

// Edit 描述对单个单元格的一次写入或清除。
type Edit struct {
	Key   string // 非空单元格键
	Value int64  // Clear 为 false 时写入的值，须在 [MinValue, MaxValue]
	Clear bool   // true 表示清除该单元格
}

// Result 是 Apply/Undo/Redo/Protect/Unprotect 的统一返回。
type Result struct {
	Code     Code   // 结果码
	Cell     string // Protected/Overwritten 时按键排序的第一个不符单元格
	Popped   bool   // 被覆盖例外：记录已从栈中弹出并丢弃
	Revision int64  // 操作被接受后的全局修订号（仅 Apply/Undo/Redo 成功时有意义）
}

// CellInfo 是 Cell 查询的返回。
type CellInfo struct {
	Value   int64 // 当前值（Set 为 false 时无意义）
	Set     bool  // 是否有值（从未写入或被清除为 false）
	Version int64 // 最近一次写入它的全局修订号，从未写入为 0
}

// HistoryInfo 是 History 查询的返回。
type HistoryInfo struct {
	UndoCount int      // 撤销栈记录条数
	RedoCount int      // 重做栈记录条数
	UndoTop   []string // 撤销栈顶记录的单元格键集合（升序），栈空为 nil
	RedoTop   []string // 重做栈顶记录的单元格键集合（升序），栈空为 nil
}
