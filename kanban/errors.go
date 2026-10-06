package kanban

import "fmt"

// ErrCode 是被拒绝操作的错误类别。
// 拒绝次序（仅报第一个）：
// 参数非法 > 时钟回退 > 卡片不存在 > 版本冲突 >
// 流转不合法 > 依赖（未完成/被依赖/成环）> 加急已占用 >
// 列上限已满 > 负责人上限已满。
type ErrCode string

const (
	ErrInvalidArgument ErrCode = "invalid_argument" // 参数非法
	ErrClockRollback   ErrCode = "clock_rollback"   // 时钟回退
	ErrCardNotFound    ErrCode = "card_not_found"   // 卡片不存在
	ErrVersionConflict ErrCode = "version_conflict" // 版本冲突
	ErrIllegalFlow     ErrCode = "illegal_flow"     // 流转不合法（越列/完成列不可动/已在原列）
	ErrDependency      ErrCode = "dependency"       // 依赖未完成 / 被依赖 / 成环 / 已存在
	ErrExpediteBusy    ErrCode = "expedite_busy"    // 加急已占用
	ErrColumnFull      ErrCode = "column_full"      // 列上限已满
	ErrOwnerFull       ErrCode = "owner_full"       // 负责人上限已满
)

// Error 携带稳定的错误码与人类可读的判定依据（reason）。
type Error struct {
	Code   ErrCode
	Reason string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Reason)
}

func newError(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Reason: fmt.Sprintf(format, args...)}
}
