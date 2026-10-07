// Package lifecycle provides tombstone/revive lifecycle coordination with
// interval-scoped provenance auditing.
package lifecycle

import "errors"

// Sentinel errors. 调用方使用 errors.Is 区分错误类别。
var (
	// ErrPermissionDenied：删除/复活操作本身的权限不足（优先级最高）。
	ErrPermissionDenied = errors.New("lifecycle: permission denied")
	// ErrStateMismatch：对象当前状态与请求操作不符（如对存活对象再次复活）。
	ErrStateMismatch = errors.New("lifecycle: object state does not match requested operation")
	// ErrObjectDeleted：对当前处于删除状态的对象发起常规读写。
	ErrObjectDeleted = errors.New("lifecycle: object is deleted")
	// ErrStaleDeleteTarget：复活指向的删除事件不是最近一次删除。
	ErrStaleDeleteTarget = errors.New("lifecycle: revive targets a delete event other than the latest one")
	// ErrLinkCondition：链接恢复条件不满足（失效时点不等于本次删除时点等）。
	ErrLinkCondition = errors.New("lifecycle: link record does not satisfy restore condition")
	// ErrNotFound：对象 / 链接 / 权限条目不存在。
	ErrNotFound = errors.New("lifecycle: not found")
)
