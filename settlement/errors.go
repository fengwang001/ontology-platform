package settlement

// errors.Is 对错误分类按“优先级相同即同类”匹配：classified 派生错误
// 与哨兵共享同一 priority，因此 errors.Is(err, ErrStateNotAllowed)
// 能命中携带明细文本的同类错误。

// 本文件定义可区分的错误分类及其固定优先级。
//
// 固定优先级（从高到低）：
//
//	参数非法 > 时钟回退 > 作业/主体/版本不存在 > 作业已结算 >
//	已超过硬性关闭时刻 > 状态不允许 > 延期超出硬性关闭 > 指定的版本无效。
//
// 所有被拒绝的操作均通过 errors.Is(err, ErrInvalidParam) 等方式判定。

// Priority 为错误类别的固定优先级，数值越小优先级越高。
type Priority int

const (
	PriorityInvalidParam       Priority = 1
	PriorityClockRollback      Priority = 2
	PriorityNotFound           Priority = 3
	PrioritySettled            Priority = 4
	PriorityAfterHardClose     Priority = 5
	PriorityStateNotAllowed    Priority = 6
	PriorityExtensionPastClose Priority = 7
	PriorityInvalidVersion     Priority = 8
)

// ClassifiedError 携带错误类别、优先级与可读信息。
type ClassifiedError struct {
	priority Priority
	message  string
}

// Error 实现 error。
func (e *ClassifiedError) Error() string { return e.message }

// Priority 返回该错误的固定优先级。
func (e *ClassifiedError) Priority() Priority { return e.priority }

// Is 使 errors.Is 按错误类别（优先级）判定，而不是按指针身份。
func (e *ClassifiedError) Is(target error) bool {
	t, ok := target.(*ClassifiedError)
	return ok && t.priority == e.priority
}

// 各类错误的哨兵值，调用方使用 errors.Is 判定类别。
var (
	ErrInvalidParam       = &ClassifiedError{PriorityInvalidParam, "invalid parameter"}
	ErrClockRollback      = &ClassifiedError{PriorityClockRollback, "clock rollback"}
	ErrNotFound           = &ClassifiedError{PriorityNotFound, "not found"}
	ErrSettled            = &ClassifiedError{PrioritySettled, "assignment already settled"}
	ErrAfterHardClose     = &ClassifiedError{PriorityAfterHardClose, "past hard close"}
	ErrStateNotAllowed    = &ClassifiedError{PriorityStateNotAllowed, "state not allowed"}
	ErrExtensionPastClose = &ClassifiedError{PriorityExtensionPastClose, "extension would exceed hard close"}
	ErrInvalidVersion     = &ClassifiedError{PriorityInvalidVersion, "designated version is invalid"}
)

func classified(base *ClassifiedError, detail string) *ClassifiedError {
	return &ClassifiedError{priority: base.priority, message: detail}
}
