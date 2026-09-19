package scope

import "errors"

// Reason 是作用域结束原因的文字说明。
type Reason string

var (
	// ErrDeadlineExceeded 表示作用域自身的截止时间已到。
	ErrDeadlineExceeded = errors.New("scope: deadline exceeded")
	// ErrCanceled 表示作用域被显式 Cancel。
	ErrCanceled = errors.New("scope: canceled")
	// ErrAncestorEnded 表示作用域因某个祖先结束而被连坐结束。
	ErrAncestorEnded = errors.New("scope: ancestor ended")
)
