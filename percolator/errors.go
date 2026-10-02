package percolator

import "fmt"

// ErrorCode identifies why an operation was rejected.
type ErrorCode string

const (
	ErrInvalidArgument ErrorCode = "invalid argument"
	ErrTxnNotFound     ErrorCode = "transaction not found"
	ErrInvalidState    ErrorCode = "invalid transaction state"
	ErrInvalidTime     ErrorCode = "invalid timestamp"
	ErrClockSkew       ErrorCode = "clock skew"
	ErrAlreadyAborted  ErrorCode = "transaction already rolled back"
	ErrKeyLocked       ErrorCode = "key locked by another transaction"
	ErrWriteConflict   ErrorCode = "write conflict"
	ErrLockLost        ErrorCode = "primary lock lost"
	ErrLocked          ErrorCode = "key is locked"
)

// Error is the typed error returned by every rejected Store operation.
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	if e.Msg == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Msg
}

func errf(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
