package ontology

import "errors"

var (
	ErrNodeNotFound      = errors.New("node not found")
	ErrEdgeAlreadyExists = errors.New("edge already exists")
	ErrEdgeLimit         = errors.New("edge limit reached")
	ErrNodeLimit         = errors.New("node limit reached")
	ErrEdgeNotFound      = errors.New("edge not found")
	ErrInvalidBatchSize  = errors.New("invalid batch size")
)

type CycleError struct {
	Witness []int
}

func (e *CycleError) Error() string {
	return "edge would create a cycle"
}

type BatchError struct {
	Index  int
	Reason error
}

func (e *BatchError) Error() string {
	return "batch operation failed"
}

func (e *BatchError) Unwrap() error {
	return e.Reason
}
