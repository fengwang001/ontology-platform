package delivery

// ErrorCode is a programmatically distinguishable failure category.
// Numeric values are stable and ordered by the documented rejection order.
type ErrorCode int

const (
	// ErrInvalidParam: constructor or argument is illegal.
	ErrInvalidParam ErrorCode = iota + 1
	// ErrClockBack: operation timestamp is earlier than the accepted maximum.
	ErrClockBack
	// ErrOrderNotFound: referenced order does not exist.
	ErrOrderNotFound
	// ErrExceptionNotFound: no matching in-progress exception exists.
	ErrExceptionNotFound
	// ErrNotPickedUp: reporting on an order that has not been picked up.
	ErrNotPickedUp
	// ErrAlreadyDelivered: order already reached delivered.
	ErrAlreadyDelivered
	// ErrExceptionClosed: acting on an exception that is already closed.
	ErrExceptionClosed
	// ErrOrderTerminal: order is in a terminal undeliverable state.
	ErrOrderTerminal
	// ErrActiveException: another exception is already in progress.
	ErrActiveException
	// ErrTypeMismatch: operation does not match the open exception's type.
	ErrTypeMismatch
	// ErrEvidenceInvalid: refusal evidence missing or out of its TTL window.
	ErrEvidenceInvalid
	// ErrContactTooFrequent: adjacent contacts are closer than the interval.
	ErrContactTooFrequent
	// ErrConditionWait: undeliverable verdict lacks the waiting duration.
	ErrConditionWait
	// ErrConditionContact: undeliverable verdict lacks enough contacts.
	ErrConditionContact
	// ErrCorrectionWindow: correction submitted after the correction window.
	ErrCorrectionWindow
	// ErrConfirmWindow: confirmation after the merchant confirmation window.
	ErrConfirmWindow
	// ErrDistanceExceeded: corrected address is too far from the original.
	ErrDistanceExceeded
)

// Error is a delivery-domain failure carrying a stable code.
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(code ErrorCode, msg string) *Error {
	return &Error{Code: code, Msg: msg}
}
