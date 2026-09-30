package deadlock

import "fmt"

// ErrCode 区分各类被拒绝操作的原因。
type ErrCode int

const (
	ErrUnknownSite ErrCode = iota
	ErrUnknownLock
	ErrUnknownTxn
	ErrTxnFinished
	ErrTxnWaiting
	ErrLockNotHeld
	ErrDuplicateTxn
	ErrDuplicateStartTS
)

func (c ErrCode) String() string {
	switch c {
	case ErrUnknownSite:
		return "unknown-site"
	case ErrUnknownLock:
		return "unknown-lock"
	case ErrUnknownTxn:
		return "unknown-txn"
	case ErrTxnFinished:
		return "txn-finished"
	case ErrTxnWaiting:
		return "txn-waiting"
	case ErrLockNotHeld:
		return "lock-not-held"
	case ErrDuplicateTxn:
		return "duplicate-txn"
	case ErrDuplicateStartTS:
		return "duplicate-start-ts"
	}
	return "unknown-error"
}

// Error 是可区分的拒绝原因，被拒绝的操作不改变任何锁表或等待关系。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

func reject(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
