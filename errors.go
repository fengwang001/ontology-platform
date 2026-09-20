package ontology

import (
	"fmt"
	"strings"
)

// ConflictError 描述一次唯一约束冲突，包含精确的定位信息：
// 违反的约束、冲突的已有记录主键、规范化后的键值，
// 以及冲突双方各自的原始值（用于展示）。
type ConflictError struct {
	// Constraint 被违反的约束名。
	Constraint string
	// Columns 该约束的属性列。
	Columns []string
	// ExistingPK 持有该键的已有记录主键。
	ExistingPK string
	// IncomingPK 被拒绝的记录主键。
	IncomingPK string
	// NormKey 规范化后的键值（各列以 " | " 连接，仅供展示）。
	NormKey string
	// IncomingValues 被拒绝记录在该约束各列上的原始值。
	IncomingValues []Value
	// ExistingValues 已有记录在该约束各列上的原始值。
	ExistingValues []Value
	// IncomingOpIndex 在批量写入中是第几条操作（从 0 计），非批量为 -1。
	IncomingOpIndex int
	// ExistingOpIndex 若冲突对象也来自同一批，是第几条操作；否则为 -1。
	ExistingOpIndex int
}

// Error 实现 error 接口。
func (e *ConflictError) Error() string {
	msg := fmt.Sprintf(
		"unique constraint %q violated on columns %v: normalized key %q already held by record %q (incoming record %q)",
		e.Constraint, e.Columns, e.NormKey, e.ExistingPK, e.IncomingPK,
	)
	if e.IncomingOpIndex >= 0 && e.ExistingOpIndex >= 0 {
		msg += fmt.Sprintf("; batch op #%d conflicts with batch op #%d", e.IncomingOpIndex, e.ExistingOpIndex)
	}
	return msg
}

// displayKey 把规范化后的各列值拼成可读的键展示形式。
func displayKey(norm []string) string {
	quoted := make([]string, len(norm))
	for i, p := range norm {
		if p == nullMarker {
			quoted[i] = "NULL"
		} else {
			quoted[i] = fmt.Sprintf("%q", p)
		}
	}
	return strings.Join(quoted, " | ")
}
