package vcs

import (
	"fmt"
	"strings"
)

// ErrCode 错误类别。声明顺序即全局优先级：一次调用触发多个错误时，
// 只报告 Code 最小（最靠前）的一个。
type ErrCode int

const (
	CodeInvalidPath         ErrCode = iota // 参数非法
	CodePathNotExist                       // 路径不存在
	CodeInConflict                         // 冲突中不可撤销/丢弃
	CodeUntrackedNeedsForce                // 未跟踪需强制
	CodeUnresolvedConflicts                // 存在未解决冲突
	CodeNothingToCommit                    // 无可提交内容
)

func (c ErrCode) String() string {
	switch c {
	case CodeInvalidPath:
		return "参数非法"
	case CodePathNotExist:
		return "路径不存在"
	case CodeInConflict:
		return "冲突中不可撤销"
	case CodeUntrackedNeedsForce:
		return "未跟踪需强制"
	case CodeUnresolvedConflicts:
		return "存在未解决冲突"
	case CodeNothingToCommit:
		return "无可提交内容"
	default:
		return "未知错误"
	}
}

// Error 是引擎返回的唯一错误类型。Path 为批操作中出错的路径；
// Paths 用于提交时列出全部未解决冲突路径。
type Error struct {
	Code   ErrCode
	Path   string
	Paths  []string
	Detail string
}

func (e *Error) Error() string {
	switch {
	case len(e.Paths) > 0:
		return fmt.Sprintf("%s: %s: [%s]", e.Code, e.Detail, strings.Join(e.Paths, ", "))
	case e.Path != "":
		return fmt.Sprintf("%s: %s: %s", e.Code, e.Detail, e.Path)
	default:
		return fmt.Sprintf("%s: %s", e.Code, e.Detail)
	}
}

func errf(code ErrCode, path, format string, args ...any) *Error {
	return &Error{Code: code, Path: path, Detail: fmt.Sprintf(format, args...)}
}

// prioritize 返回优先级最高（Code 最小）的错误；并列时保留先出现者。
func prioritize(errs []*Error) *Error {
	var best *Error
	for _, e := range errs {
		if e == nil {
			continue
		}
		if best == nil || e.Code < best.Code {
			best = e
		}
	}
	return best
}
