package ontology

import "fmt"

// Code 是可区分的重命名/解析失败原因。
type Code string

const (
	// CodeTypeNotFound 表示要操作的类型名从未注册过。
	CodeTypeNotFound Code = "TYPE_NOT_FOUND"
	// CodeOldNameInvalidated 表示该名字曾因重命名而失效，不可再解析。
	CodeOldNameInvalidated Code = "OLD_NAME_INVALIDATED"
	// CodeNameConflict 表示新名字已被其它类型占用。
	CodeNameConflict Code = "NAME_CONFLICT"
	// CodeResidualReference 表示重命名后图中仍残留对旧名的引用。
	CodeResidualReference Code = "RESIDUAL_REFERENCE"
	// CodeOldNameResolvable 表示重命名后旧名仍可解析到原类型。
	CodeOldNameResolvable Code = "OLD_NAME_STILL_RESOLVABLE"
	// CodeDanglingReference 表示图中存在指向不存在类型的悬空引用。
	CodeDanglingReference Code = "DANGLING_REFERENCE"
	// CodeRenameAborted 表示重命名中断，已整体回滚，未产生半改状态。
	CodeRenameAborted Code = "RENAME_ABORTED"
)

// Error 携带可区分的失败原因及判定依据。
type Error struct {
	Code     Code
	TypeName string
	Reason   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: type=%q: %s", e.Code, e.TypeName, e.Reason)
}

func newError(code Code, typeName, reason string) *Error {
	return &Error{Code: code, TypeName: typeName, Reason: reason}
}
