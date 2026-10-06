package ontology

type ErrorCode string

const (
	ErrInvalidArgument ErrorCode = "invalid_argument"
	ErrClockRewind     ErrorCode = "clock_rewind"
	ErrNotFound        ErrorCode = "not_found"
	ErrOverlap         ErrorCode = "overlap"
	ErrInvalidState    ErrorCode = "invalid_state"
	ErrAmountRange     ErrorCode = "amount_out_of_range"
)

type ServiceError struct {
	Code    ErrorCode
	Message string
}

func (e *ServiceError) Error() string { return string(e.Code) + ": " + e.Message }

func fail(code ErrorCode, message string) error { return &ServiceError{Code: code, Message: message} }
