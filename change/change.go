// Package change 定义基表变更记录及其校验。
package change

import (
	"errors"
	"math"
)

// Op 是变更操作类型。
type Op uint8

const (
	OpInsert Op = iota + 1
	OpDelete
	OpUpdate
)

func (o Op) String() string {
	switch o {
	case OpInsert:
		return "insert"
	case OpDelete:
		return "delete"
	case OpUpdate:
		return "update"
	}
	return "unknown"
}

// 可判定的校验错误。
var (
	ErrBadOp     = errors.New("change: unknown op")
	ErrNoGroup   = errors.New("change: missing group key")
	ErrNaN       = errors.New("change: value is NaN")
	ErrBadLength = errors.New("change: group key too long")
)

// Change 是一条基表变更。Group 为 nil 表示分组键缺失（非法，拒绝并
// 计数）；指向空串是合法的空分组键。Delete 只需要 ID，Group 可为 nil。
type Change struct {
	Version uint64
	Op      Op
	ID      uint64
	Group   *string
	Value   float64
}

// Str 返回 s 的指针，便于构造字面量。
func Str(s string) *string { return &s }

// Validate 校验单条变更的静态合法性（不依赖视图状态）。
func (c Change) Validate() error {
	switch c.Op {
	case OpInsert, OpUpdate:
		if c.Group == nil {
			return ErrNoGroup
		}
		if len(*c.Group) > maxGroupLen {
			return ErrBadLength
		}
		if math.IsNaN(c.Value) {
			return ErrNaN
		}
	case OpDelete:
	default:
		return ErrBadOp
	}
	return nil
}

// Equal 报告两条变更是否逐字段相等（用于幂等重放判定）。
func (c Change) Equal(o Change) bool {
	if c.Version != o.Version || c.Op != o.Op || c.ID != o.ID ||
		c.Value != o.Value {
		return false
	}
	if c.Group == nil || o.Group == nil {
		return c.Group == nil && o.Group == nil
	}
	return *c.Group == *o.Group
}
