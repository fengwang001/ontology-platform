package topk

// Kind 标识维护器拒绝一次操作的具体原因。
type Kind int

const (
	KindInvalidK Kind = iota + 1
	KindCapacityTooSmall
	KindEmptyID
	KindCapacityExceeded
)

// Error 携带可程序化区分的失败原因。
type Error struct {
	Kind Kind
	msg  string
}

func (e *Error) Error() string {
	return e.msg
}

var (
	// ErrInvalidK 表示 K 不是正整数。
	ErrInvalidK = &Error{Kind: KindInvalidK, msg: "topk: K must be positive"}
	// ErrCapacityTooSmall 表示容量小于 K。
	ErrCapacityTooSmall = &Error{Kind: KindCapacityTooSmall, msg: "topk: capacity must be >= K"}
	// ErrEmptyID 表示元素标识为空字符串。
	ErrEmptyID = &Error{Kind: KindEmptyID, msg: "topk: element ID must not be empty"}
	// ErrCapacityExceeded 表示新标识加入时已达容量上限。
	ErrCapacityExceeded = &Error{Kind: KindCapacityExceeded, msg: "topk: capacity exceeded by new ID"}
)
