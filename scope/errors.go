package scope

import "errors"

// Reason 是作用域结束原因的文字说明。
type Reason string

// 三类结束原因，均可用 errors.Is 判定。
var (
	// ErrDeadlineExceeded 表示自身截止时间到。
	ErrDeadlineExceeded = errors.New("scope: deadline exceeded")
	// ErrCanceled 表示自身被显式 Cancel。
	ErrCanceled = errors.New("scope: canceled")
	// ErrAncestorEnded 表示因某个祖先结束而连坐。
	ErrAncestorEnded = errors.New("scope: ancestor ended")
)
