package ontology

import (
	"errors"
	"fmt"
)

// 哨兵错误，供 errors.Is 判断错误类别。
var (
	ErrNotFound        = errors.New("ontology: instance not found")
	ErrDeleted         = errors.New("ontology: instance logically deleted")
	ErrVersionConflict = errors.New("ontology: version conflict")
	ErrAlreadyExists   = errors.New("ontology: instance already exists")
	ErrValidation      = errors.New("ontology: property constraint violation")
	ErrDuplicateKey    = errors.New("ontology: duplicate primary key in batch")
)

// NotFoundError 表示主键从未存在过（对 Get/Update/Delete 而言）。
type NotFoundError struct {
	ObjectType string
	PrimaryKey string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("ontology: instance %s/%s not found", e.ObjectType, e.PrimaryKey)
}

func (e *NotFoundError) Is(target error) bool { return target == ErrNotFound }

// DeletedError 表示实例已被逻辑删除。它与 NotFoundError 是不同类别：
// 主键存在过、版本历史仍保留，但当前不可读写。
type DeletedError struct {
	ObjectType string
	PrimaryKey string
	Version    int64 // 删除发生时推进到的版本号
}

func (e *DeletedError) Error() string {
	return fmt.Sprintf("ontology: instance %s/%s has been deleted (at version %d)",
		e.ObjectType, e.PrimaryKey, e.Version)
}

func (e *DeletedError) Is(target error) bool { return target == ErrDeleted }

// AlreadyExistsError 表示 Create 时主键已被存活实例占用。
type AlreadyExistsError struct {
	ObjectType string
	PrimaryKey string
	Version    int64 // 当前存活实例的版本号
}

func (e *AlreadyExistsError) Error() string {
	return fmt.Sprintf("ontology: instance %s/%s already exists (version %d)",
		e.ObjectType, e.PrimaryKey, e.Version)
}

func (e *AlreadyExistsError) Is(target error) bool { return target == ErrAlreadyExists }

// ConflictError 表示乐观并发控制失败：调用方携带的期望版本号
// 与存储中的实际版本号不一致。
type ConflictError struct {
	ObjectType string
	PrimaryKey string
	Expected   int64 // 调用方读到的期望版本号
	Actual     int64 // 存储中的当前实际版本号
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("ontology: version conflict on %s/%s: expected version %d, actual version %d",
		e.ObjectType, e.PrimaryKey, e.Expected, e.Actual)
}

func (e *ConflictError) Is(target error) bool { return target == ErrVersionConflict }

// ValidationError 表示写入的属性违反约束。
type ValidationError struct {
	ObjectType string
	PrimaryKey string
	Property   string
	Reason     string
}

func (e *ValidationError) Error() string {
	if e.Property == "" {
		return fmt.Sprintf("ontology: invalid instance %s/%s: %s", e.ObjectType, e.PrimaryKey, e.Reason)
	}
	return fmt.Sprintf("ontology: invalid property %q on %s/%s: %s",
		e.Property, e.ObjectType, e.PrimaryKey, e.Reason)
}

func (e *ValidationError) Is(target error) bool { return target == ErrValidation }

// DuplicateKeyError 表示同一批次中同一主键出现了多次。
type DuplicateKeyError struct {
	ObjectType string
	PrimaryKey string
	FirstIndex int // 该主键第一次出现的批次下标
}

func (e *DuplicateKeyError) Error() string {
	return fmt.Sprintf("ontology: duplicate primary key %s/%s in batch (first seen at op %d)",
		e.ObjectType, e.PrimaryKey, e.FirstIndex)
}

func (e *DuplicateKeyError) Is(target error) bool { return target == ErrDuplicateKey }

// BatchError 包装批次中第一条失败操作的错误，指出是第几条、
// 哪个主键、哪一类原因（通过 Unwrap 暴露底层错误）。
type BatchError struct {
	Index      int
	ObjectType string
	PrimaryKey string
	Err        error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("ontology: batch write failed at op %d (%s/%s): %v",
		e.Index, e.ObjectType, e.PrimaryKey, e.Err)
}

func (e *BatchError) Unwrap() error { return e.Err }
