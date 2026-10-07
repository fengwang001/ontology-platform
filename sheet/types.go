// Package sheet 实现多人协同表格的按用户选择性撤销与重做服务。
package sheet

// 取值与时间范围约束。
const (
	// MaxValue 是单元格允许写入的最大绝对值（10^15）。
	MaxValue = int64(1_000_000_000_000_000)
	// MaxNow 是 now 允许的最大值（10^12 秒）。
	MaxNow = int64(1_000_000_000_000)
	// MaxEdits 是单次 Apply 允许的最大编辑条数。
	MaxEdits = 50
	// MinDepth 是每用户历史深度 D 的最小值。
	MinDepth = 1
	// MaxDepth 是每用户历史深度 D 的最大值。
	MaxDepth = 100
)

// Edit 表示对单个单元格的一次写入或清除。
// Clear 为 true 时表示清除，Value 被忽略；否则表示写入 Value。
type Edit struct {
	Key   string
	Value int64
	Clear bool
}

// ErrCode 标识操作被拒绝的类别，也是日志中的判定依据。
type ErrCode string

const (
	CodeOK            ErrCode = "OK"
	CodeInvalidParam  ErrCode = "InvalidParam"
	CodeClockRollback ErrCode = "ClockRollback"
	CodeProtected     ErrCode = "Protected"
	CodeNoChange      ErrCode = "NoChange"
	CodeEmptyStack    ErrCode = "EmptyStack"
	CodeOverwritten   ErrCode = "Overwritten"
)

// Result 是所有变更类操作的统一返回。
type Result struct {
	// OK 为 true 表示操作被接受。
	OK bool
	// Code 为 OK 或拒绝类别。
	Code ErrCode
	// Cell 在 Protected / Overwritten 时给出按键排序的第一个冲突单元格。
	Cell string
	// Dropped 标明“被覆盖例外”：记录已从栈顶弹出并丢弃（不进入重做栈）。
	// 这是被拒绝操作中唯一允许改变状态的例外。
	Dropped bool
	// Revision 是操作结束后的全局修订号（被拒绝时保持原值）。
	Revision int64
}

func okResult(revision int64) Result {
	return Result{OK: true, Code: CodeOK, Revision: revision}
}

func reject(code ErrCode, revision int64) Result {
	return Result{OK: false, Code: code, Revision: revision}
}

func rejectCell(code ErrCode, cell string, revision int64) Result {
	return Result{OK: false, Code: code, Cell: cell, Revision: revision}
}

// CellState 是 Cell 查询的返回：值（可能为空）与版本。
type CellState struct {
	Value   int64
	Empty   bool
	Version int64
}

// CellChange 记录事务中单个单元格的原值、新值与期望版本。
type CellChange struct {
	Key      string
	OldValue int64
	OldEmpty bool
	NewValue int64
	NewEmpty bool
	// Version 是该记录生效后该单元格的版本，也是后续 Undo/Redo 的期望版本。
	Version int64
}

// Record 是一条历史记录（一个事务）。Changes 始终按键升序排列，
// 以保证“按键排序的第一个冲突单元格”的确定性。
type Record struct {
	Changes []CellChange
}

func (r Record) keys() []string {
	keys := make([]string, len(r.Changes))
	for i, c := range r.Changes {
		keys[i] = c.Key
	}
	return keys
}

// HistoryInfo 是 History 查询的返回。
type HistoryInfo struct {
	UndoCount int
	RedoCount int
	// UndoTop / RedoTop 是各自栈顶记录的单元格键集合（升序）；栈空时为 nil。
	UndoTop []string
	RedoTop []string
}

func validNow(now int64) bool { return now >= 0 && now <= MaxNow }

func validValue(v int64) bool { return v >= -MaxValue && v <= MaxValue }
