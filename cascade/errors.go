package cascade

// Kind 表示一次被拒绝操作的错误类别。优先级从高到低：
// KindInvalidArgument > KindNotFound > KindConflict > KindCycle > KindOwnerMissing。
type Kind int

const (
	KindInvalidArgument Kind = iota + 1
	KindNotFound
	KindConflict
	KindCycle
	KindOwnerMissing
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "InvalidArgument"
	case KindNotFound:
		return "NotFound"
	case KindConflict:
		return "Conflict"
	case KindCycle:
		return "Cycle"
	case KindOwnerMissing:
		return "OwnerMissing"
	default:
		return "Unknown"
	}
}

// GCError 是控制器对外暴露的唯一错误类型，携带可区分的错误类别。
type GCError struct {
	Kind Kind
	Msg  string
}

func (e *GCError) Error() string {
	return e.Kind.String() + ": " + e.Msg
}

func newError(kind Kind, msg string) *GCError {
	return &GCError{Kind: kind, Msg: msg}
}
