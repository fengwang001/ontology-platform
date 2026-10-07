package ontology

import "fmt"

// FailureKind enumerates the mutually exclusive reasons for which a batch
// can be rejected. The detection order mandated by the specification is:
// DuplicateDeclaration, then VersionConflict, then ValidationRejected,
// then CardinalityViolated. A batch reports exactly one kind.
type FailureKind string

const (
	FailureNone               FailureKind = ""
	FailureDuplicateDecl      FailureKind = "duplicate_declaration"
	FailureVersionConflict    FailureKind = "version_conflict"
	FailureValidationRejected FailureKind = "validation_rejected"
	FailureCardinality        FailureKind = "cardinality_violated"
)

// BatchError is returned by Store.ApplyBatch when a batch is rejected.
// Kind is always one of the four mutually exclusive failure kinds. Index is
// the zero-based position of the offending item inside BatchInput.Items and
// is populated for every kind (duplicate declarations carry the second
// occurrence index; Detail carries the first occurrence).
type BatchError struct {
	Kind   FailureKind
	Index  int
	Detail string
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("ontology: batch rejected: %s at item %d: %s", e.Kind, e.Index, e.Detail)
}

func duplicateError(secondIndex, firstIndex int, id InstanceID) *BatchError {
	return &BatchError{
		Kind:   FailureDuplicateDecl,
		Index:  secondIndex,
		Detail: fmt.Sprintf("instance %q is declared by items %d and %d", id, firstIndex, secondIndex),
	}
}

func versionConflictError(index int, id InstanceID, declared, current Version) *BatchError {
	return &BatchError{
		Kind:   FailureVersionConflict,
		Index:  index,
		Detail: fmt.Sprintf("instance %q baseline %d != current %d", id, declared, current),
	}
}

func validationError(index int, reason string) *BatchError {
	return &BatchError{
		Kind:   FailureValidationRejected,
		Index:  index,
		Detail: reason,
	}
}

func cardinalityError(index int, detail string) *BatchError {
	return &BatchError{
		Kind:   FailureCardinality,
		Index:  index,
		Detail: detail,
	}
}
