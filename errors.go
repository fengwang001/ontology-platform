package ontology

import "fmt"

// NotFoundError 表示主键从未存在过（或 Get 视角下不可见）。
type NotFoundError struct {
	ObjectType string
	PrimaryKey string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("ontology: %s/%s not found", e.ObjectType, e.PrimaryKey)
}

// DeletedError 表示实例已被逻辑删除。它不会退化为 NotFoundError
// 或 VersionConflictError，调用方可用 errors.As 区分。
type DeletedError struct {
	ObjectType string
	PrimaryKey string
	// Version 是逻辑删除发生时的版本号（墓碑版本）。
	Version int64
}

func (e *DeletedError) Error() string {
	return fmt.Sprintf("ontology: %s/%s deleted at version %d",
		e.ObjectType, e.PrimaryKey, e.Version)
}

// VersionConflictError 表示期望版本与当前实际版本不一致。
type VersionConflictError struct {
	ObjectType string
	PrimaryKey string
	Expected   int64
	Actual     int64
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("ontology: %s/%s version conflict: expected %d, actual %d",
		e.ObjectType, e.PrimaryKey, e.Expected, e.Actual)
}

// AlreadyExistsError 表示对存活主键重复 Create。
type AlreadyExistsError struct {
	ObjectType string
	PrimaryKey string
	// Version 是现存实例的当前版本。
	Version int64
}

func (e *AlreadyExistsError) Error() string {
	return fmt.Sprintf("ontology: %s/%s already exists at version %d",
		e.ObjectType, e.PrimaryKey, e.Version)
}

// ConstraintError 表示写入的属性违反了约束。
type ConstraintError struct {
	ObjectType string
	PrimaryKey string
	Reason     string
}

func (e *ConstraintError) Error() string {
	return fmt.Sprintf("ontology: %s/%s constraint violation: %s",
		e.ObjectType, e.PrimaryKey, e.Reason)
}

// BatchError 指出批量写中失败的具体条目。
type BatchError struct {
	// Index 是该操作在批次中的下标（从 0 开始）。
	Index      int
	PrimaryKey string
	// Err 是具体原因（*VersionConflictError / *DeletedError 等）。
	Err error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("ontology: batch op %d (%s): %v", e.Index, e.PrimaryKey, e.Err)
}

func (e *BatchError) Unwrap() error { return e.Err }
