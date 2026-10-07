package ontology

// This file is a compilable skeleton.

// ErrorKind enumerates the distinct, stably-ordered adjudication failures.
type ErrorKind int

const (
	ErrNone ErrorKind = iota
	// ErrInstanceNotFound is reported for a missing instance.
	ErrInstanceNotFound
	// ErrRowInvisible: the subject fails the merged row-level predicate.
	// Writes against invisible instances return the same opaque not-found
	// kind, so existence of a hidden instance cannot be probed.
	ErrRowInvisible
	// ErrPropertyNotWritable: at least one targeted property is not
	// writable under RejectAll write mode.
	ErrPropertyNotWritable
	// ErrMaskedTypeViolation: a masking/derivation function produced a
	// value violating the property's declared type contract.
	ErrMaskedTypeViolation
	// ErrInvalidValueType: a supplied write value violates the declared
	// type contract before any masking is applied.
	ErrInvalidValueType
	// ErrUnknownProperty: a referenced property is not declared.
	ErrUnknownProperty
	// ErrInvalidType: an object-type declaration is malformed.
	ErrInvalidType
)

func (k ErrorKind) String() string {
	switch k {
	case ErrInstanceNotFound:
		return "ErrInstanceNotFound"
	case ErrRowInvisible:
		return "ErrRowInvisible"
	case ErrPropertyNotWritable:
		return "ErrPropertyNotWritable"
	case ErrMaskedTypeViolation:
		return "ErrMaskedTypeViolation"
	case ErrInvalidValueType:
		return "ErrInvalidValueType"
	case ErrUnknownProperty:
		return "ErrUnknownProperty"
	case ErrInvalidType:
		return "ErrInvalidType"
	default:
		return "ErrNone"
	}
}

// DecisionError is an adjudication failure with a fixed, unique kind.
type DecisionError struct {
	Kind ErrorKind
	Msg  string
}

func (e *DecisionError) Error() string { return e.Msg }
