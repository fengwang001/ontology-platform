package ontology

import "errors"

var (
	ErrSchema           = errors.New("ontology: invalid schema")
	ErrParam            = errors.New("ontology: invalid parameter")
	ErrType             = errors.New("ontology: type mismatch")
	ErrMissingRequired  = errors.New("ontology: missing required field")
	ErrUnknownField     = errors.New("ontology: unknown field")
	ErrTooLarge         = errors.New("ontology: record produces too many entries")
	ErrInconsistentCols = errors.New("ontology: inconsistent columns")
)

// FieldError 携带错误种类与点号路径。
type FieldError struct {
	Kind error
	Path string
}

func (e *FieldError) Error() string {
	return e.Kind.Error() + ": " + e.Path
}

func (e *FieldError) Unwrap() error {
	return e.Kind
}

// ErrorPath 取出错误中的点号路径；非本包错误返回 ""。
func ErrorPath(err error) string {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe.Path
	}
	return ""
}
