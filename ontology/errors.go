package ontology

// RejectError is a distinguishable, stable error category.
type RejectError struct {
	Code    string
	Message string
}

func (r *RejectError) Error() string { return r.Code + ": " + r.Message }

// Error categories for rejected submissions. All rejections are atomic:
// the global state and the sent-counter never change. Each category has a
// distinct, stable Code so callers can tell the rejection reasons apart.
var (
	ErrInvalidOperation = &RejectError{"E_INVALID_OPERATION", "operation must be insert or retract"}
	ErrEmptyGroupName   = &RejectError{"E_EMPTY_GROUP_NAME", "group name must not be empty"}
	ErrRetractNotFound  = &RejectError{"E_RETRACT_NOT_FOUND", "retract references a row that does not exist"}
	ErrTooManyGroups    = &RejectError{"E_TOO_MANY_GROUPS", "submission would exceed the group limit"}
	ErrDuplicateRow     = &RejectError{"E_DUPLICATE_ROW", "row id already exists or is repeated in the batch"}
	ErrInvalidValue     = &RejectError{"E_INVALID_VALUE", "value must parse as a finite decimal number"}
)
