package ontology

import "errors"

// Reason classifies a rejected batch.
type Reason string

const (
	ReasonInvalidArgument Reason = "invalid_argument"
	ReasonInvalidEvent    Reason = "invalid_event"
	ReasonLimitExceeded   Reason = "limit_exceeded"
)

// BatchError reports why a batch was rejected without side effects.
type BatchError struct {
	Reason Reason
	Index  int
	Msg    string
}

func (e *BatchError) Error() string {
	return string(e.Reason) + ": " + e.Msg
}

// AsBatchError extracts a *BatchError from err.
func AsBatchError(err error) (*BatchError, bool) {
	var be *BatchError
	if errors.As(err, &be) {
		return be, true
	}
	return nil, false
}
