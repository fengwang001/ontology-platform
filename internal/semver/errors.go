package semver

import (
	"errors"
	"fmt"
)

// 哨兵错误，调用方可使用 errors.Is 判定。
var (
	// ErrInvalidVersion 表示版本字符串不符合 SemVer 2.0.0 语法。
	ErrInvalidVersion = errors.New("semver: invalid version")
	// ErrInvalidRange 表示范围约束字符串无法解析。
	ErrInvalidRange = errors.New("semver: invalid range")
	// ErrEmptyIntersection 表示多个范围的交集为空。
	ErrEmptyIntersection = errors.New("semver: empty intersection")
)

// ParseError 携带解析失败时的原始输入与出错位置说明。
type ParseError struct {
	Kind  error  // ErrInvalidVersion 或 ErrInvalidRange
	Input string // 原始输入
	Msg   string // 具体原因
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s: %q: %s", e.Kind, e.Input, e.Msg)
}

func (e *ParseError) Unwrap() error { return e.Kind }

// ConflictError 表示交集为空，并指明互相冲突的两条具体约束。
type ConflictError struct {
	Left  Constraint
	Right Constraint
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("constraints %q and %q conflict (intersection is empty)",
		e.Left.Raw, e.Right.Raw)
}

func (e *ConflictError) Unwrap() error { return ErrEmptyIntersection }
