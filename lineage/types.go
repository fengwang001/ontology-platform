package lineage

import (
	"errors"
	"fmt"
)

// Rename 表示提交中显式声明的一条「旧路径 -> 新路径」改名记录。
type Rename struct {
	Old string
	New string
}

// Commit 是一次提交的不可变描述。Files 为该提交的完整快照（路径 -> 内容）。
type Commit struct {
	ID      string
	Parents []string
	Files   map[string]string
	Renames []Rename
}

// Attribution 是单行的归属结果：该行内容由哪个提交、以什么路径、第几行首次引入。
// IgnoredButAttributed 为 true 表示归属提交在忽略名单中，但该行确实由它引入。
type Attribution struct {
	CommitID             string
	Path                 string
	Line                 int
	IgnoredButAttributed bool
}

// Kind 区分错误类别，调用方可用 IsKind 判定。
type Kind int

const (
	KindInvalidParam Kind = iota // 参数非法（空标识、空路径、负版本、忽略名单含不存在的提交等）
	KindDuplicateCommit
	KindParentNotFound
	KindInvalidRename
	KindCommitNotFound
	KindVersionNotFound
	KindPathNotFound
)

// Error 是服务返回的唯一错误类型。
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(k Kind, format string, args ...any) *Error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, args...)}
}

// IsKind 报告 err 是否为指定类别的错误。
func IsKind(err error, k Kind) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == k
}
