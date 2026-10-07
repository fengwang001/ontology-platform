// Package importguard implements an attribute-level permission gatekeeper
// for batch imports in an ontology platform.
package importguard

// ErrorCategory identifies the distinct, independently attributable failure
// classes produced by a batch import.
type ErrorCategory string

const (
	// ErrBatchInvalidParameter is a batch-level rejection: the import mode
	// parameter is missing or illegal. When it occurs no record is processed.
	ErrBatchInvalidParameter ErrorCategory = "batch_invalid_parameter"
	// ErrSubjectNotFound is a batch-level rejection: the initiating subject
	// does not exist at batch start. When it occurs no record is processed.
	ErrSubjectNotFound ErrorCategory = "subject_not_found"
	// ErrObjectTypeNotFound is a record-level rejection: the referenced object
	// type does not exist.
	ErrObjectTypeNotFound ErrorCategory = "object_type_not_found"
	// ErrSemanticMismatch is a record-level rejection: the declared
	// create/update semantic does not match the actual existence state of the
	// target object (e.g. updating a non-existent object).
	ErrSemanticMismatch ErrorCategory = "semantic_mismatch"
	// ErrFieldPermissionDenied is a record-level rejection used in atomic mode:
	// at least one field of the record is not writable by the subject, so the
	// whole record fails.
	ErrFieldPermissionDenied ErrorCategory = "field_permission_denied"
	// ErrRequiredPropertyMissing is a record-level rejection: after field
	// skipping in lenient mode, a required property cannot be satisfied
	// (neither written nor pre-existing), so the record is fully rolled back.
	ErrRequiredPropertyMissing ErrorCategory = "required_property_missing"
)

// Failure is an attributed record-level or batch-level error.
type Failure struct {
	Category ErrorCategory
	Message  string
	// Fields carries the involved field names when the category is field
	// related (denied fields, or required properties left missing).
	Fields []string
}

func (f *Failure) Error() string { return f.Message }
