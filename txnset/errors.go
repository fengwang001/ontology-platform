package txnset

import (
	"errors"
	"strconv"
)

// ErrorKind 标识解析错误的类别，便于调用方按类别区分处理。
type ErrorKind int

const (
	// KindSyntax 语法错误：空白、分隔符缺失/多余、空条目、空边界等。
	KindSyntax ErrorKind = iota + 1
	// KindIdentifier 来源标识非法：首字符、字符合法集或长度不符合规则。
	KindIdentifier
	// KindNumber 事务号非法：非数字、前导零、越界或区间下限大于上限。
	KindNumber
	// KindTooManyIntervals 单次解析的区间总数超过 MaxIntervalCount。
	KindTooManyIntervals
)

func (k ErrorKind) String() string {
	switch k {
	case KindSyntax:
		return "syntax error"
	case KindIdentifier:
		return "illegal source identifier"
	case KindNumber:
		return "illegal transaction number"
	case KindTooManyIntervals:
		return "interval count exceeds limit"
	default:
		return "unknown error"
	}
}

// ParseError 是 Parse / MergeText 返回的具体错误类型。
// Offset 为错误在输入文本中从左到右的首个字节偏移；KindTooManyIntervals
// 的偏移指向超限条目的起点。
type ParseError struct {
	Kind    ErrorKind
	Offset  int
	Message string
}

func (e *ParseError) Error() string {
	return "txnset: " + e.Kind.String() + " at offset " +
		strconv.Itoa(e.Offset) + ": " + e.Message
}

// Is 使 errors.Is 可按类别判定：
//
//	errors.Is(err, txnset.ErrSyntax)
//	errors.Is(err, txnset.ErrIdentifier)
//	errors.Is(err, txnset.ErrNumber)
//	errors.Is(err, txnset.ErrTooManyIntervals)
func (e *ParseError) Is(target error) bool {
	switch target {
	case ErrSyntax:
		return e.Kind == KindSyntax
	case ErrIdentifier:
		return e.Kind == KindIdentifier
	case ErrNumber:
		return e.Kind == KindNumber
	case ErrTooManyIntervals:
		return e.Kind == KindTooManyIntervals
	default:
		return false
	}
}

func newParseError(kind ErrorKind, offset int, msg string) *ParseError {
	return &ParseError{Kind: kind, Offset: offset, Message: msg}
}

// 哨兵错误，供 errors.Is 按类别判定。
var (
	ErrSyntax           = errors.New("txnset: syntax error")
	ErrIdentifier       = errors.New("txnset: illegal source identifier")
	ErrNumber           = errors.New("txnset: illegal transaction number")
	ErrTooManyIntervals = errors.New("txnset: interval count exceeds limit")
)
