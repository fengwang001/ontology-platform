package policy

// Error kinds reported by the arbitration engine. Their enumeration order
// defines the fixed reporting priority:
// missing reference > direct visibility conflict > masking cycle > type violation.

import "fmt"

// Kind identifies the class of an arbitration error. Kinds are listed in the
// fixed, unique reporting priority mandated by the specification:
// the engine evaluates a request in four phases and reports the first failing
// phase only. Type-violation errors are the sole exception: they are scoped to
// a single attribute and reported inside a successful Result so that other
// attributes (and other subjects) are unaffected.
type Kind int

const (
	// KindMissingReference: an attribute, policy or referenced input does not exist.
	KindMissingReference Kind = iota + 1
	// KindVisibilityConflict: allow and deny decisions cannot be reconciled.
	KindVisibilityConflict
	// KindMaskingCycle: masking derivation rules reference each other cyclically.
	KindMaskingCycle
	// KindTypeViolation: a derived value violates the attribute's declared type.
	KindTypeViolation
)

// String returns the stable, human-readable kind name.
func (k Kind) String() string {
	switch k {
	case KindMissingReference:
		return "MissingReference"
	case KindVisibilityConflict:
		return "VisibilityConflict"
	case KindMaskingCycle:
		return "MaskingCycle"
	case KindTypeViolation:
		return "TypeViolation"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Error is the common error type carrying a distinct Kind so callers can
// discriminate failures programmatically with errors.As.
type Error struct {
	Kind Kind
	// Msg describes the precise cause, including offending identifiers.
	Msg string
	// Cycle holds the ordered attributes participating in a cycle, when Kind
	// is KindMaskingCycle; nil otherwise.
	Cycle []string
}

func (e *Error) Error() string {
	if e.Kind == KindMaskingCycle && len(e.Cycle) > 0 {
		return fmt.Sprintf("policy: %s: %s (cycle: %v)", e.Kind, e.Msg, e.Cycle)
	}
	return fmt.Sprintf("policy: %s: %s", e.Kind, e.Msg)
}

func newMissingReference(format string, args ...any) error {
	return &Error{Kind: KindMissingReference, Msg: fmt.Sprintf(format, args...)}
}

func newVisibilityConflict(format string, args ...any) error {
	return &Error{Kind: KindVisibilityConflict, Msg: fmt.Sprintf(format, args...)}
}

// Is reports errors equal by Kind, so errors.Is(err, &Error{Kind: k}) works.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

// AttributeError is a per-attribute failure. It never aborts the presentation
// of other attributes; attribute order is preserved by the enclosing Result.
type AttributeError struct {
	Attr string
	Err  *Error
}

func (a *AttributeError) Error() string {
	return fmt.Sprintf("attribute %q: %s", a.Attr, a.Err.Error())
}
