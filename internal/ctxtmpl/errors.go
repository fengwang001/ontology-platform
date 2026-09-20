package ctxtmpl

// Sentinel errors. Callers can use errors.Is to classify failures.
var (
	// ErrInvalidSyntax is returned when an interpolation is malformed or
	// its delimiters are not paired.
	ErrInvalidSyntax = sentinel("ctxtmpl: invalid template syntax")
	// ErrMissingKey is returned when the template references a key that
	// is absent from the data map.
	ErrMissingKey = sentinel("ctxtmpl: template key not found in data")
	// ErrInterpolationInTagName is returned when an interpolation appears
	// where an attribute name or other tag-internal position is expected.
	ErrInterpolationInTagName = sentinel("ctxtmpl: interpolation not allowed in tag name position")
	// ErrUnclosedTag is returned when the template ends inside a tag.
	ErrUnclosedTag = sentinel("ctxtmpl: unclosed tag at end of template")
	// ErrUnclosedQuote is returned when the template ends inside a quoted
	// attribute value.
	ErrUnclosedQuote = sentinel("ctxtmpl: unclosed attribute quote at end of template")
	// ErrUnsafeURL is returned when an interpolated URL begins with a
	// disallowed scheme such as javascript:.
	ErrUnsafeURL = sentinel("ctxtmpl: unsafe URL scheme in attribute value")
)

// sentinel is a constant-flavored error type supporting errors.Is.
type sentinel string

func (s sentinel) Error() string { return string(s) }

// TemplateError wraps a sentinel with a template position for diagnostics.
type TemplateError struct {
	Err error
	Pos int
	Key string
}

func (e *TemplateError) Error() string {
	if e.Key != "" {
		return e.Err.Error() + ": " + e.Key
	}
	return e.Err.Error()
}

func (e *TemplateError) Unwrap() error { return e.Err }
