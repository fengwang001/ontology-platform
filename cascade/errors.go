package cascade

// ErrorCode enumerates the mutually exclusive request-level errors.
type ErrorCode int

const (
	ErrObjectNotFound ErrorCode = iota + 1
	ErrRestricted
	ErrUndefinedLinkType
)

// CascadeError is returned when a deletion request cannot complete.
type CascadeError struct {
	Code   ErrorCode
	Detail string
}

func (e *CascadeError) Error() string {
	switch e.Code {
	case ErrObjectNotFound:
		return "cascade: object not found: " + e.Detail
	case ErrRestricted:
		return "cascade: deletion restricted by link " + e.Detail
	default:
		return "cascade: undefined link type: " + e.Detail
	}
}
