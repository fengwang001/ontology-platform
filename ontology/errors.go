package ontology

import "errors"

// 可区分的拒绝原因。
var (
	// ErrEmptyGroup 表示行的组名为空。
	ErrEmptyGroup = errors.New("ontology: empty group name")
	// ErrEmptyRowID 表示行的 ID 为空。
	ErrEmptyRowID = errors.New("ontology: empty row id")
	// ErrDeleteMissing 表示删除一条不存在的行。
	ErrDeleteMissing = errors.New("ontology: delete references missing row")
	// ErrDuplicateInsert 表示插入已存在的行 ID。
	ErrDuplicateInsert = errors.New("ontology: duplicate row id")
	// ErrTooManyGroups 表示批处理后组数超过上限。
	ErrTooManyGroups = errors.New("ontology: group count exceeds limit")
)

// RejectError 携带可区分原因与出错位置。
type RejectError struct {
	Err     error
	Index   int
	RowID   string
	Group   string
	Limit   int
	Current int
}

func (e *RejectError) Error() string { return e.Err.Error() }
func (e *RejectError) Unwrap() error { return e.Err }

type configError struct {
	msg string
}

func (e *configError) Error() string { return e.msg }
