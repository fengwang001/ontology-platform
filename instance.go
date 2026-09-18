package ontology

import (
	"fmt"
	"time"
)

// Instance 是一个对象实例的快照。Properties 为独立副本，
// 调用方修改不会影响存储，存储的后续写入也不会影响它。
type Instance struct {
	ObjectType string
	PrimaryKey string
	Version    int64
	UpdatedAt  time.Time
	Properties map[string]any
}

// OpKind 表示 BatchWrite 中单条操作的类型。
type OpKind int

const (
	OpCreate OpKind = iota
	OpUpdate
	OpDelete
)

func (k OpKind) String() string {
	switch k {
	case OpCreate:
		return "create"
	case OpUpdate:
		return "update"
	case OpDelete:
		return "delete"
	default:
		return fmt.Sprintf("op(%d)", int(k))
	}
}

// WriteOp 是 BatchWrite 中的一条写操作。
// ExpectedVersion 仅 OpUpdate / OpDelete 使用；
// Properties 仅 OpCreate / OpUpdate 使用。
type WriteOp struct {
	Kind            OpKind
	ObjectType      string
	PrimaryKey      string
	ExpectedVersion int64
	Properties      map[string]any
}

// NotFoundError 表示主键从未存在过（或对 Get 而言当前不存在）。
type NotFoundError struct {
	ObjectType string
	PrimaryKey string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("ontology: instance %s/%s not found", e.ObjectType, e.PrimaryKey)
}

// DeletedError 表示实例已被逻辑删除。它与 NotFoundError 是不同类别：
// 主键曾经存在，版本历史仍然保留。
type DeletedError struct {
	ObjectType string
	PrimaryKey string
	Version    int64 // 删除时的版本号
}

func (e *DeletedError) Error() string {
	return fmt.Sprintf("ontology: instance %s/%s was deleted at version %d", e.ObjectType, e.PrimaryKey, e.Version)
}

// VersionConflictError 表示期望版本与当前实际版本不一致。
type VersionConflictError struct {
	ObjectType string
	PrimaryKey string
	Expected   int64
	Actual     int64
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("ontology: version conflict on %s/%s: expected %d, actual %d",
		e.ObjectType, e.PrimaryKey, e.Expected, e.Actual)
}

// AlreadyExistsError 表示 Create 的目标主键当前已存在（未删除）。
type AlreadyExistsError struct {
	ObjectType string
	PrimaryKey string
	Version    int64
}

func (e *AlreadyExistsError) Error() string {
	return fmt.Sprintf("ontology: instance %s/%s already exists at version %d", e.ObjectType, e.PrimaryKey, e.Version)
}

// ConstraintError 表示写入的属性违反约束。
type ConstraintError struct {
	ObjectType string
	PrimaryKey string
	Field      string
	Reason     string
}

func (e *ConstraintError) Error() string {
	return fmt.Sprintf("ontology: constraint violation on %s/%s field %q: %s",
		e.ObjectType, e.PrimaryKey, e.Field, e.Reason)
}

// DuplicateOperationError 表示同一批中同一主键出现了多次操作。
type DuplicateOperationError struct {
	ObjectType string
	PrimaryKey string
	FirstIndex int // 该主键在批中第一次出现的位置
}

func (e *DuplicateOperationError) Error() string {
	return fmt.Sprintf("ontology: duplicate operation for %s/%s in batch (first at index %d)",
		e.ObjectType, e.PrimaryKey, e.FirstIndex)
}

// BatchError 包装批中第 Index 条操作的失败原因，
// 指出是哪一条、哪个主键、哪一类原因（Err 为上述具体错误类型之一）。
type BatchError struct {
	Index      int
	Op         OpKind
	ObjectType string
	PrimaryKey string
	Err        error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("ontology: batch op #%d (%s %s/%s) failed: %v",
		e.Index, e.Op, e.ObjectType, e.PrimaryKey, e.Err)
}

func (e *BatchError) Unwrap() error { return e.Err }
