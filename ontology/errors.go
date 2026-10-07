package ontology

import "errors"

type ErrorCode int

const (
	ErrObjectNotFound ErrorCode = iota + 1
	ErrPropertyNotIndexed
	ErrIndexMaintenance
	ErrBatchRollback
)

type WriteError struct {
	Code   ErrorCode
	Object string
	Reason error
}

func (e *WriteError) Error() string { return e.message() }
func (e *WriteError) Unwrap() error { return e.Reason }

var errIndexMaintenance = errors.New("index maintenance failed")

func writeError(code ErrorCode, object string, reason error) *WriteError {
	return &WriteError{Code: code, Object: object, Reason: reason}
}

func priorityError(errs []*WriteError) *WriteError {
	if len(errs) == 0 {
		return nil
	}
	best := errs[0]
	for _, candidate := range errs[1:] {
		if candidate.Code < best.Code {
			best = candidate
		}
	}
	return best
}

type BatchError struct {
	Errors []*WriteError
}

func (e *BatchError) Error() string {
	return priorityError(e.Errors).Error()
}

func (e *BatchError) Unwrap() []error {
	out := make([]error, len(e.Errors))
	for i, err := range e.Errors {
		out[i] = err
	}
	return out
}
