package ontology

import "errors"

// 可区分的整体拒绝原因。调用方可用 errors.Is 判定。
var (
	ErrEmptyName     = errors.New("ontology: empty name")
	ErrUnknownDep    = errors.New("ontology: unknown dependency name")
	ErrUnknownBase   = errors.New("ontology: unknown base name")
	ErrCycle         = errors.New("ontology: dependency cycle detected")
	ErrTooManyViews  = errors.New("ontology: too many views")
	ErrInvalidLimit  = errors.New("ontology: invalid view limit")
	ErrDuplicateName = errors.New("ontology: duplicate base/view name")
	ErrNotFound      = errors.New("ontology: name not found")
)
