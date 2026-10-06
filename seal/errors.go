// Package seal implements an electronic seal usage-control service:
// authorization management, tiered approval, execution-time re-check,
// two-custodian presence confirmation and post-usage receipt/freeze lifecycle.
//
// Time is measured in integer seconds. See design.md for the full semantics.
package seal

// ErrorCode identifies a rejected operation.
type ErrorCode string

const (
	// ErrInvalidParameter: malformed or self-contradicting input.
	ErrInvalidParameter ErrorCode = "INVALID_PARAMETER"
	// ErrClockBackward: now is smaller than a previously accepted now.
	ErrClockBackward ErrorCode = "CLOCK_BACKWARD"
	// ErrNotFound: referenced application or seal does not exist.
	ErrNotFound ErrorCode = "NOT_FOUND"
	// ErrStateNotAllowed: operation is not legal in the entity's current state.
	ErrStateNotAllowed ErrorCode = "STATE_NOT_ALLOWED"
	// ErrFrozen: the applicant is frozen due to overdue receipts.
	ErrFrozen ErrorCode = "FROZEN"
	// ErrNoGrant: the applicant holds no effective grant for the seal.
	ErrNoGrant ErrorCode = "NO_EFFECTIVE_GRANT"
	// ErrLimit: material category not permitted or amount above the per-use cap.
	ErrLimit ErrorCode = "CATEGORY_OR_AMOUNT_LIMIT"
	// ErrDailyLimit: daily successful-use count has reached the grant cap.
	ErrDailyLimit ErrorCode = "DAILY_LIMIT_REACHED"
	// ErrMissingPresence: two distinct custodian confirmations are required but absent.
	ErrMissingPresence ErrorCode = "MISSING_PRESENCE_CONFIRMATION"
)

// SealError is returned for every rejected operation. Rejections never mutate state.
type SealError struct {
	Code   ErrorCode
	Reason string
}

func (e *SealError) Error() string {
	return string(e.Code) + ": " + e.Reason
}

func sealErr(code ErrorCode, reason string) *SealError {
	return &SealError{Code: code, Reason: reason}
}
