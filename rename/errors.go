package rename

import "fmt"

// Rename 表示一条旧名 -> 新名的映射。
type Rename struct {
	Old string
	New string
}

// Reason 是批次被整体拒绝的可区分原因。
type Reason string

const (
	ReasonEmptyName    Reason = "empty_name"
	ReasonDuplicateOld Reason = "duplicate_old"
	ReasonDuplicateNew Reason = "duplicate_new"
	ReasonOldNotFound  Reason = "old_not_found"
	ReasonNewOccupied  Reason = "new_already_occupied"
)

// ValidationError 描述批次在校验阶段被整体拒绝的原因。
type ValidationError struct {
	Reason      Reason
	Detail      string
	Rename      Rename
	Conflicting string
}

func (e *ValidationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return string(e.Reason) + ": " + e.Detail
	}
	return string(e.Reason)
}

// ExecError 描述执行过程中某一步失败。
type ExecError struct {
	Step int
	From string
	To   string
	Err  error
}

func (e *ExecError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("rename step %d (%q -> %q) failed: %v", e.Step, e.From, e.To, e.Err)
}
func (e *ExecError) Unwrap() error { return e.Err }

// UndoError 描述撤销请求被拒绝的原因。
type UndoError struct {
	Reason string
	Detail string
}

func (e *UndoError) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail != "" {
		return e.Reason + ": " + e.Detail
	}
	return e.Reason
}
