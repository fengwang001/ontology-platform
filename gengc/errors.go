package gengc

// GCError 描述分代回收器所有可拒绝的操作，分类由 Kind 唯一确定。
type GCError struct {
	Kind  ErrorKind
	Op    string
	Field int
	ID    uint64
}

// ErrorKind 是错误类别。拒绝次序：未定义 > 参数 > 悬垂 > 空间不足。
type ErrorKind int

const (
	ErrUndefined ErrorKind = iota + 1
	ErrInvalidArgument
	ErrDangling
	ErrOutOfSpace
)

func (e *GCError) Error() string {
	return e.Kind.String()
}

func (k ErrorKind) String() string {
	switch k {
	case ErrUndefined:
		return "undefined reference"
	case ErrInvalidArgument:
		return "invalid argument"
	case ErrDangling:
		return "dangling reference"
	case ErrOutOfSpace:
		return "out of space"
	default:
		return "unknown error"
	}
}

func errf(kind ErrorKind, op string, id uint64, field int) *GCError {
	return &GCError{Kind: kind, Op: op, Field: field, ID: id}
}
