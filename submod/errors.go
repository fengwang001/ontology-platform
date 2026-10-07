package submod

import "fmt"

// ErrCode 是可区分的错误类别。前八个的先后关系即规范的错误次序：
// 参数非法 < 循环挂载 < 路径冲突 < 仓库不存在 < 分支不存在 <
// 悬空固定 < 有未提交修改 < 跟踪分支非快进。
type ErrCode int

const (
	ErrCodeInvalidParam   ErrCode = iota + 1 // 参数非法（空路径、空仓库标识等）
	ErrCodeCircularMount                     // 循环挂载
	ErrCodePathConflict                      // 路径冲突（相同或祖先后代）
	ErrCodeRepoNotFound                      // 仓库不存在
	ErrCodeBranchNotFound                    // 分支不存在
	ErrCodeDanglingPin                       // 悬空固定（固定提交不存在于目标仓库）
	ErrCodeDirtyWorkspace                    // 有未提交修改
	ErrCodeNonFastForward                    // 跟踪分支非快进
	ErrCodeMountNotFound                     // 挂载点不存在（移除/查询时）
	ErrCodeInvalidTable                      // 子模块表非法（载入时）
)

var errCodeNames = map[ErrCode]string{
	ErrCodeInvalidParam:   "参数非法",
	ErrCodeCircularMount:  "循环挂载",
	ErrCodePathConflict:   "路径冲突",
	ErrCodeRepoNotFound:   "仓库不存在",
	ErrCodeBranchNotFound: "分支不存在",
	ErrCodeDanglingPin:    "悬空固定",
	ErrCodeDirtyWorkspace: "有未提交修改",
	ErrCodeNonFastForward: "跟踪分支非快进",
	ErrCodeMountNotFound:  "挂载点不存在",
	ErrCodeInvalidTable:   "子模块表非法",
}

func (c ErrCode) String() string { return errCodeNames[c] }

// Error 是协调器返回的可区分错误。
type Error struct {
	Code ErrCode
	Path string // 相关挂载路径（可空）
	Msg  string
}

func (e *Error) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("submod: %s: %s (%s)", e.Code, e.Msg, e.Path)
	}
	return fmt.Sprintf("submod: %s: %s", e.Code, e.Msg)
}

// errf 构造一个带路径的错误。
func errf(code ErrCode, path, format string, args ...any) *Error {
	return &Error{Code: code, Path: path, Msg: fmt.Sprintf(format, args...)}
}
