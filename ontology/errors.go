package ontology

import (
	"errors"
	"fmt"
)

// 错误类别哨兵，调用方用 errors.Is 区分。
var (
	// ErrNotFound 主键从未存在过。
	ErrNotFound = errors.New("ontology: instance does not exist")
	// ErrDeleted 实例已被逻辑删除（版本历史仍保留）。与 ErrNotFound 是不同类别。
	ErrDeleted = errors.New("ontology: instance has been deleted")
	// ErrAlreadyExists Create 时主键已存在（且未被删除）。
	ErrAlreadyExists = errors.New("ontology: instance already exists")
	// ErrInvalidArgument ObjectType / PrimaryKey 为空或操作类型非法。
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	// ErrConstraintViolation 写入的属性违反约束。
	ErrConstraintViolation = errors.New("ontology: property constraint violation")
	// ErrDuplicateInBatch 同一批里同一个 (ObjectType, PrimaryKey) 出现多次。
	ErrDuplicateInBatch = errors.New("ontology: duplicate primary key in batch")
)

// VersionConflictError 期望版本与当前实际版本不一致。
// 调用方可通过 errors.As 取出，分辨自己期望的版本与存储中的实际版本。
type VersionConflictError struct {
	ObjectType string
	PrimaryKey string
	Expected   int64 // 调用方读到的期望版本
	Actual     int64 // 存储中的当前实际版本
}

func (e *VersionConflictError) Error() string {
	return fmt.Sprintf("ontology: version conflict on %s/%s: expected version %d, actual version %d",
		e.ObjectType, e.PrimaryKey, e.Expected, e.Actual)
}

// OpKind 是 BatchWrite 中单条操作的类型。
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
		return "unknown"
	}
}

// BatchError 描述 BatchWrite 中第 Index 条操作失败，整批已回滚。
// Err 是具体原因（ErrNotFound / ErrDeleted / *VersionConflictError /
// ErrAlreadyExists / ErrConstraintViolation / ErrDuplicateInBatch 等），
// 可用 errors.Is / errors.As 继续分辨。
type BatchError struct {
	Index      int
	Op         OpKind
	ObjectType string
	PrimaryKey string
	Err        error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("ontology: batch write failed at op %d (%s %s/%s): %v",
		e.Index, e.Op, e.ObjectType, e.PrimaryKey, e.Err)
}

func (e *BatchError) Unwrap() error { return e.Err }
