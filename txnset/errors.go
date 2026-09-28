package txnset

import (
	"errors"
	"fmt"
)

// Kind 标识解析失败的具体类别，使语法错误、标识非法、数值非法、
// 区间总数超限可被调用方明确区分。
type Kind int

const (
	// KindUnknown 为未分类错误（正常不会出现）。
	KindUnknown Kind = iota
	// KindSyntax 语法错误：非法字符、空白、分隔符缺失或多余等。
	KindSyntax
	// KindIdentifier 来源标识非法。
	KindIdentifier
	// KindNumber 事务号非法：前导零、溢出、区间端点倒置等。
	KindNumber
	// KindTooMany 区间总数超过 MaxIntervals。
	KindTooMany
)

// String 返回错误类别的简短英文名。
func (k Kind) String() string {
	switch k {
	case KindSyntax:
		return "syntax error"
	case KindIdentifier:
		return "invalid source identifier"
	case KindNumber:
		return "invalid transaction number"
	case KindTooMany:
		return "too many intervals"
	default:
		return "unknown error"
	}
}

// ParseError 是解析文本时返回的错误，按从左到右的顺序报告第一个错误。
type ParseError struct {
	// Kind 错误类别。
	Kind Kind
	// Pos 出错位置的字节偏移（从 0 开始），即从左到右第一个非法位置。
	Pos int
	// Msg 人类可读的说明。
	Msg string
}

// Error 实现 error，形如 "syntax error at position 3: ..."。
func (e *ParseError) Error() string {
	if e.Msg == "" {
		return fmt.Sprintf("%s at position %d", e.Kind, e.Pos)
	}
	return fmt.Sprintf("%s at position %d: %s", e.Kind, e.Pos, e.Msg)
}

// Is 使两个 *ParseError 在类别相同时互相匹配。
func (e *ParseError) Is(target error) bool {
	var pe *ParseError
	if errors.As(target, &pe) {
		return pe.Kind == e.Kind
	}
	return false
}

// Unwrap 返回与类别对应的哨兵错误，使 errors.Is(err, ErrSyntax) 生效。
func (e *ParseError) Unwrap() error {
	switch e.Kind {
	case KindSyntax:
		return ErrSyntax
	case KindIdentifier:
		return ErrIdentifier
	case KindNumber:
		return ErrNumber
	case KindTooMany:
		return ErrTooMany
	default:
		return nil
	}
}

// parseErrorAt 构造指定位置与类别的解析错误。
func parseErrorAt(kind Kind, pos int, format string, args ...any) *ParseError {
	return &ParseError{Kind: kind, Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

// 可区分的哨兵错误，配合 errors.Is 使用。
var (
	// ErrSyntax 语法错误。
	ErrSyntax = sentinelError("syntax error")
	// ErrIdentifier 来源标识非法。
	ErrIdentifier = sentinelError("invalid source identifier")
	// ErrNumber 事务号非法。
	ErrNumber = sentinelError("invalid transaction number")
	// ErrTooMany 区间总数超限。
	ErrTooMany = sentinelError("too many intervals")
)

// sentinelError 是哨兵错误的最小载体。
type sentinelError string

// Error 实现 error。
func (e sentinelError) Error() string { return string(e) }
