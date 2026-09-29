package sampling

// ErrorKind 对拒绝原因做稳定分类，互不相同、可区分。
type ErrorKind int

const (
	ErrNone ErrorKind = iota
	ErrRateOutOfRange
	ErrEmptyKey
	ErrTooManyKnownKeys
)

// SamplingError 携带机器可读的错误类别与上下文信息。
type SamplingError struct {
	Kind ErrorKind
	Msg  string
}

func (e *SamplingError) Error() string { return e.Msg }

func newError(kind ErrorKind, msg string) *SamplingError {
	return &SamplingError{Kind: kind, Msg: msg}
}
