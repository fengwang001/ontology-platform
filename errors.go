package ontology

import "fmt"

// ConflictError 表示一次唯一约束冲突：写入被拒绝。
type ConflictError struct {
	// Constraint 被违反的约束名。
	Constraint string
	// Key 冲突发生的规范化键（可读形式）。
	Key string
	// ExistingPK 持有该键的已有记录主键。
	ExistingPK string
	// Incoming 写入方在约束属性上的原始值（未规范化，nil 表示 NULL）。
	Incoming map[string]*string
	// Existing 已有记录在约束属性上的原始值（未规范化，nil 表示 NULL）。
	Existing map[string]*string
	// OpIndex 批量写入中引发冲突的操作下标；非批量写入为 -1。
	OpIndex int
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("unique constraint %q violated: normalized key {%s} already held by record %q",
		e.Constraint, e.Key, e.ExistingPK)
}

// BatchConflictError 表示批量写入内部两条操作互相冲突。
type BatchConflictError struct {
	// First、Second 是冲突两条操作在批次中的下标（First < Second）。
	First, Second int
	// Constraint 被违反的约束名。
	Constraint string
	// Key 冲突发生的规范化键（可读形式）。
	Key string
	// FirstPK、SecondPK 是两条操作各自携带的主键。
	FirstPK, SecondPK string
}

func (e *BatchConflictError) Error() string {
	return fmt.Sprintf("batch ops #%d (pk %q) and #%d (pk %q) conflict on unique constraint %q: normalized key {%s}",
		e.First, e.FirstPK, e.Second, e.SecondPK, e.Constraint, e.Key)
}

// BatchOpError 表示批量写入中某条操作自身非法（如主键重复）。
type BatchOpError struct {
	OpIndex int
	Err     error
}

func (e *BatchOpError) Error() string {
	return fmt.Sprintf("batch op #%d invalid: %v", e.OpIndex, e.Err)
}

func (e *BatchOpError) Unwrap() error { return e.Err }
