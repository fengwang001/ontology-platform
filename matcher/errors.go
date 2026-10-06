package matcher

// ErrorCode classifies a rejected operation so callers can distinguish
// failure causes programmatically.
type ErrorCode int

const (
	ErrInvalidParameter ErrorCode = iota + 1
	ErrDuplicateID
	ErrOrderNotFound
	ErrOrderFinished
)

// EngineError is returned for every rejected operation. Rejected operations
// never modify engine state and never consume a sequence number.
type EngineError struct {
	Code    ErrorCode
	Message string
}

func (e *EngineError) Error() string {
	return e.Message
}

func errInvalid(msg string) *EngineError {
	return &EngineError{Code: ErrInvalidParameter, Message: msg}
}

func errDuplicate(id string) *EngineError {
	return &EngineError{Code: ErrDuplicateID, Message: "duplicate order id: " + id}
}

func errNotFound(id string) *EngineError {
	return &EngineError{Code: ErrOrderNotFound, Message: "order not found: " + id}
}

func errFinished(id string) *EngineError {
	return &EngineError{Code: ErrOrderFinished, Message: "order already filled or cancelled: " + id}
}
