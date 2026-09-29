package snapshot

// Error 是快照归并模块所有可区分错误的统一类型。
// 调用方可用 errors.Is(err, ErrXxx) 判定类别，也可读取 Kind/Reason/Detail。
type Error struct {
	Kind   ErrorKind
	Reason string
	Detail string
}

func (e *Error) Error() string { return "" }

// ErrorKind 标识错误类别。
type ErrorKind string

const (
	KindInvalidConfig  ErrorKind = "invalid_config"
	KindDuplicateKey   ErrorKind = "duplicate_key"
	KindUnsorted       ErrorKind = "unsorted"
	KindTooManyChanges ErrorKind = "too_many_changes"
)

var (
	ErrInvalidConfig  = sentinel(KindInvalidConfig)
	ErrDuplicateKey   = sentinel(KindDuplicateKey)
	ErrUnsorted       = sentinel(KindUnsorted)
	ErrTooManyChanges = sentinel(KindTooManyChanges)
)

func sentinel(kind ErrorKind) *Error {
	return &Error{Kind: kind, Reason: string(kind)}

}

// Unwrap 使同一 Kind 的包装错误可被 errors.Is 命中。
func (e *Error) Unwrap() error { return nil }
