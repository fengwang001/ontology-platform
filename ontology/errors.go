package ontology

import "fmt"

// IndexError 携带可区分的错误原因。具体类型见各错误变量。
type IndexError struct {
	kind    string
	message string
}

func (e *IndexError) Error() string { return e.kind + ": " + e.message }
func (e *IndexError) Kind() string  { return e.kind }

// Is 使 errors.Is 能按错误类别匹配各哨兵错误，即使错误携带了定制信息。
func (e *IndexError) Is(target error) bool {
	t, ok := target.(*IndexError)
	return ok && t.kind == e.kind
}

func newIndexError(kind, format string, args ...any) error {
	return &IndexError{kind: kind, message: fmt.Sprintf(format, args...)}
}

// 以下哨兵错误可通过 errors.Is 判定；IndexError.Kind() 返回对应错误字符串。
var (
	ErrInvalidSize       = &IndexError{kind: "invalid size", message: "root side must be a positive power of two"}
	ErrInvalidCapacity   = &IndexError{kind: "invalid capacity", message: "capacity must be positive"}
	ErrPointOutOfRange   = &IndexError{kind: "point out of range", message: "point lies outside the root region"}
	ErrEmptyID           = &IndexError{kind: "empty id", message: "point id must not be empty"}
	ErrDuplicateID       = &IndexError{kind: "duplicate id", message: "point id already exists or repeats in batch"}
	ErrDeleteNotFound    = &IndexError{kind: "id not found", message: "cannot delete an id that does not exist"}
	ErrInvalidQueryRange = &IndexError{kind: "invalid query range", message: "query rectangle has malformed bounds"}
)
