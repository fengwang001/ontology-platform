package ontology

import "fmt"

// DecisionErrorKind distinguishes every rejection the adjudicator can emit.
// The three policy-derived kinds have a fixed reporting precedence on the
// write path: ErrRowInvisible > ErrPropertyNotWritable > ErrMaskTypeViolation.
// ErrUnknownProperty is a request-shape error reported before policy
// adjudication and can never coexist with a policy verdict.
type DecisionErrorKind string

const (
	// ErrRowInvisible: the subject may not see the instance at all. It is
	// returned both for genuinely missing and for policy-hidden instances, so
	// its existence cannot be inferred.
	ErrRowInvisible DecisionErrorKind = "row_invisible"
	// ErrPropertyNotWritable: instance is visible but a written property is
	// denied by the property-level write verdict (WriteReject mode).
	ErrPropertyNotWritable DecisionErrorKind = "property_not_writable"
	// ErrMaskTypeViolation: a masking result fails the property's declared
	// type contract — on read or on write after the mask is applied.
	ErrMaskTypeViolation DecisionErrorKind = "mask_type_violation"
	// ErrUnknownProperty: the request names a property the type does not
	// declare. Never reported together with a policy decision.
	ErrUnknownProperty DecisionErrorKind = "unknown_property"
	// ErrValueTypeViolation: a submitted write value violates the declared
	// type before any policy is consulted.
	ErrValueTypeViolation DecisionErrorKind = "value_type_violation"
)

// DecisionError carries the unique machine-readable kind plus diagnostics.
type DecisionError struct {
	Kind       DecisionErrorKind
	ObjectType string
	InstanceID string
	Property   string
	Message    string
}

func (e *DecisionError) Error() string {
	if e.Property != "" {
		return fmt.Sprintf("%s: %s/%s property %q: %s",
			e.Kind, e.ObjectType, e.InstanceID, e.Property, e.Message)
	}
	return fmt.Sprintf("%s: %s/%s: %s",
		e.Kind, e.ObjectType, e.InstanceID, e.Message)
}

func newError(kind DecisionErrorKind, typeName, id, property, msg string) *DecisionError {
	return &DecisionError{
		Kind:       kind,
		ObjectType: typeName,
		InstanceID: id,
		Property:   property,
		Message:    msg,
	}
}
