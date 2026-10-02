package ontology

import "errors"

// Sentinel error kinds. Use errors.Is to classify errors.
var (
	ErrSchema          = errors.New("ontology: invalid schema")
	ErrParam           = errors.New("ontology: invalid parameter")
	ErrType            = errors.New("ontology: value type mismatch")
	ErrMissingRequired = errors.New("ontology: missing required field")
	ErrUnknownField    = errors.New("ontology: unknown field")
	ErrTooLarge        = errors.New("ontology: record produces more entries than maxEntries")
	ErrInconsistent    = errors.New("ontology: inconsistent columns during assembly")
)

// FieldError attaches a dotted schema path to a classified error kind.
type FieldError struct {
	Kind error
	Path string
}

func (e *FieldError) Error() string {
	if e.Path == "" {
		return e.Kind.Error()
	}
	return e.Kind.Error() + ": " + e.Path
}

func (e *FieldError) Unwrap() error { return e.Kind }

func fieldErr(kind error, path string) error {
	return &FieldError{Kind: kind, Path: path}
}

// ErrorPath returns the dotted path carried by an error, if any.
func ErrorPath(err error) string {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe.Path
	}
	return ""
}
