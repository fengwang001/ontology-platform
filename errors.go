package ontology

// ErrorClass identifies the category of a rejected operation.
//
// The engine checks error conditions in a fixed, unique priority order:
//
//	ClassUnknownLinkType
//	ClassObjectNotFound
//	ClassTypeMismatch
//	ClassLinkExists / ClassLinkNotFound
//	ClassCardinalityViolation
//	ClassPermissionDenied
//	ClassCascadeAborted
//
// In particular, when a cardinality violation and a permission denial
// both hold, ClassCardinalityViolation is always the one reported.
type ErrorClass int

const (
	ClassNone ErrorClass = iota
	ClassUnknownLinkType
	ClassObjectNotFound
	ClassTypeMismatch
	ClassLinkExists
	ClassLinkNotFound
	ClassCardinalityViolation
	ClassPermissionDenied
	ClassCascadeAborted
)

func (c ErrorClass) String() string {
	switch c {
	case ClassNone:
		return "none"
	case ClassUnknownLinkType:
		return "unknown_link_type"
	case ClassObjectNotFound:
		return "object_not_found"
	case ClassTypeMismatch:
		return "type_mismatch"
	case ClassLinkExists:
		return "link_exists"
	case ClassLinkNotFound:
		return "link_not_found"
	case ClassCardinalityViolation:
		return "cardinality_violation"
	case ClassPermissionDenied:
		return "permission_denied"
	case ClassCascadeAborted:
		return "cascade_aborted"
	default:
		return "unknown"
	}
}

// OpError describes a rejected operation.
type OpError struct {
	Class ErrorClass
	Msg   string
}

func (e *OpError) Error() string { return e.Class.String() + ": " + e.Msg }
