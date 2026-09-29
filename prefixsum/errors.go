package prefixsum

// ErrorKind 标识一次被拒绝操作的错误类别。
type ErrorKind int

const (
	// KindInvalidArgument 参数非法（如 View 选项非法）。
	KindInvalidArgument ErrorKind = iota + 1
	// KindKeyOutOfRange 键超出允许范围。
	KindKeyOutOfRange
	// KindKeyNotFound 键不存在。
	KindKeyNotFound
	// KindKeyLimitExceeded 键的数量超过上限。
	KindKeyLimitExceeded
	// KindSumOverflow 前缀和或写入值发生整数溢出。
	KindSumOverflow
)

// ViewError 携带可区分的错误类别，可通过 errors.Is 与哨兵错误比较。
type ViewError struct {
	Kind ErrorKind
	Msg  string
}

func (e *ViewError) Error() string { return e.Msg }
