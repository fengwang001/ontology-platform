package lineage

import (
	"errors"
	"fmt"
)

// 可区分的拒绝原因（使用 errors.Is 判定）。
var (
	// ErrNoLineage：派生操作未记录任何输入血缘。
	ErrNoLineage = errors.New("lineage: 派生缺少血缘记录（至少需要一个输入）")
	// ErrRefNotFound：血缘引用的对象或版本不存在。
	ErrRefNotFound = errors.New("lineage: 血缘引用不存在的对象或版本")
	// ErrStaleVersion：血缘指向已过期（被新版本取代）的版本。
	ErrStaleVersion = errors.New("lineage: 血缘指向过期版本")
	// ErrMissingUpstream：派生对象缺少上游血缘。
	ErrMissingUpstream = errors.New("lineage: 血缘查询漏上游（派生节点无输入边）")
	// ErrMissingDownstream：上游仍在使用的当前版本缺少反向（下游）血缘。
	ErrMissingDownstream = errors.New("lineage: 血缘查询漏下游（输入边缺少反向记录）")
	// ErrKindConflict：同一 ID 已以另一种产生方式存在。
	ErrKindConflict = errors.New("lineage: 对象产生方式冲突（源对象与派生对象不能复用同一 ID）")
)

// RejectError 携带被拒绝操作的上下文，便于日志与调用方区分原因。
type RejectError struct {
	Cause    error
	Op       string
	Ref      *Ref
	Evidence string // 判定依据
}

func (e *RejectError) Error() string {
	if e.Ref != nil {
		return fmt.Sprintf("%s: op=%s ref=%s@%s 依据=%s",
			e.Cause, e.Op, e.Ref.ID, e.Ref.Version, e.Evidence)
	}
	return fmt.Sprintf("%s: op=%s 依据=%s", e.Cause, e.Op, e.Evidence)
}

func (e *RejectError) Unwrap() error { return e.Cause }

func reject(cause error, op string, ref *Ref, evidence string) *RejectError {
	return &RejectError{Cause: cause, Op: op, Ref: ref, Evidence: evidence}
}
