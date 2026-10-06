package bus

// ErrorKind 是可区分的错误类别，判定顺序见各入口校验。
type ErrorKind int

const (
	ErrInvalidParam ErrorKind = iota + 1
	ErrClockRollback
	ErrTripNotFound
	ErrStopNotFound
	ErrOutOfOrder
	ErrDuplicateReport
	ErrNotBunched
	ErrDriverNotFound
)

// BusError 携带稳定的错误类别与可读原因。
type BusError struct {
	Kind ErrorKind
	Msg  string
}

func (e *BusError) Error() string { return e.Msg }

func kindError(k ErrorKind, msg string) *BusError {
	return &BusError{Kind: k, Msg: msg}
}
