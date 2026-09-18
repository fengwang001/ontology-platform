package instance

import "fmt"

var (
	errBadOpKind     = fmt.Errorf("unknown batch operation kind")
	errEmptyIdentity = fmt.Errorf("object type and key must be non-empty")
)

func batchErr(index int, op Op, reason Reason, expected, actual int64, cause error) *BatchError {
	return &BatchError{
		Index:      index,
		ObjectType: op.ObjectType,
		Key:        op.Key,
		Reason:     reason,
		Expected:   expected,
		Actual:     actual,
		Err:        cause,
	}
}

func duplicateErr(first int) error {
	return fmt.Errorf("same primary key also appears at index %d; batch entries must not be merged", first)
}
