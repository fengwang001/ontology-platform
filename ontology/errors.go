package ontology

import "errors"

var (
	// ErrInvalidArgument：参数非法（深度/扇出为负、续读标记格式不合法）。
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	// ErrForbidden：起点对象对调用者无存在性权限（受限不可遍历）。
	ErrForbidden = errors.New("ontology: access to start object is forbidden")
	// ErrTokenObsolete：续读标记锚定的起点对象在当前状态下已不存在。
	ErrTokenObsolete = errors.New("ontology: continuation token is no longer traceable")
)
