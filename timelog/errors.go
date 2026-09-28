package timelog

// ErrorKind 标识一次被拒绝操作的具体原因，互不相同、可区分。
type ErrorKind int

const (
	// KindInvalidArgument 参数本身非法（空批次、nil 消息、容量配置非法等）。
	KindInvalidArgument ErrorKind = iota + 1
	// KindNegativeTimestamp 消息时间戳为负。
	KindNegativeTimestamp
	// KindCapacityExceeded 追加会超出日志配置容量。
	KindCapacityExceeded
)

// Error 描述一次被整体拒绝的操作，原因由 Kind 给出。
type Error struct {
	Kind   ErrorKind
	Op     string
	Detail string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return "timelog: " + e.Op + ": " + e.Detail
}

func newError(kind ErrorKind, op, detail string) *Error {
	return &Error{Kind: kind, Op: op, Detail: detail}
}

// Is 允许 errors.Is(err, ErrInvalidArgument) 等按类别判定。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Kind == t.Kind
}

var (
	// ErrInvalidArgument 参数本身非法。
	ErrInvalidArgument = &Error{Kind: KindInvalidArgument}
	// ErrNegativeTimestamp 存在负时间戳。
	ErrNegativeTimestamp = &Error{Kind: KindNegativeTimestamp}
	// ErrCapacityExceeded 容量超限。
	ErrCapacityExceeded = &Error{Kind: KindCapacityExceeded}
)
