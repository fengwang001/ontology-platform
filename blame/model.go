// Package blame 实现面向提交图的行归属追溯服务。
//
// 版本库由提交构成：每个提交有唯一标识、零个或多个父提交、
// 路径到文件内容的快照、以及若干条「旧路径到新路径」的改名记录。
// 服务对任一提交中任一文件的每一行，判定该行内容由哪个提交、
// 以什么路径首次引入，并支持可版本化的忽略名单与并发读写。
//
// 行号约定：对外行号从 1 开始。文件内容按 "\n" 切分行，
// 末尾的换行符不产生额外的空行；空文件有 0 行。行比较逐字节进行。
package blame

import "errors"

// Rename 表示一条「旧路径到新路径」的改名记录。
type Rename struct {
	OldPath string
	NewPath string
}

// Commit 是一次提交的完整输入。载入后服务内部持有其副本，不可修改。
type Commit struct {
	ID      string
	Parents []string
	Files   map[string]string
	Renames []Rename
}

// Attribution 是单行的归属结果：归属三元组及「被忽略仍归属」标记。
type Attribution struct {
	CommitID             string
	Path                 string
	Line                 int // 归属行号，1 起始，指在归属提交中的行号
	IgnoredButAttributed bool
}

// ErrorKind 区分错误类别，查询按类别次序只报最靠前的一个。
type ErrorKind int

const (
	KindInvalidParam    ErrorKind = iota // 参数非法（空标识、空路径、负版本、名单含未知提交等）
	KindDuplicateCommit                  // 同一标识重复载入
	KindParentNotFound                   // 父提交不存在
	KindInvalidRename                    // 改名记录非法
	KindCommitNotFound                   // 查询的提交不存在
	KindVersionNotFound                  // 名单版本不存在
	KindPathNotFound                     // 路径在该提交中不存在
)

// Error 是服务返回的统一错误类型。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// KindOf 取出错误的类别；非本服务错误返回 false。
func KindOf(err error) (ErrorKind, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind, true
	}
	return 0, false
}

func newError(kind ErrorKind, msg string) *Error {
	return &Error{Kind: kind, Msg: msg}
}
