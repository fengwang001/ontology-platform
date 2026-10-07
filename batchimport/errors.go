package batchimport

import (
	"errors"
	"strconv"
)

// Kind 是归一化后的错误类别，三类错误必须可相互区分。
type Kind int

const (
	// KindInvalidParam 参数非法（主键重复、字段类型不符、未知对象类型等）。
	KindInvalidParam Kind = iota + 1
	// KindPreHook 单条记录的前置校验钩子失败。
	KindPreHook
	// KindPostHook 批次级后置校验钩子失败（仅全有或全无语义）。
	KindPostHook
)

// Error 是归一化后的导入错误。
type Error struct {
	Kind   Kind
	Index  int // 关联的输入列表下标；批次级错误为 -1
	Reason string
}

func (e *Error) Error() string {
	var where string
	if e.Index >= 0 {
		where = "record[" + strconv.Itoa(e.Index) + "]: "
	}
	return e.Kind.label() + ": " + where + e.Reason
}

func (k Kind) label() string {
	switch k {
	case KindInvalidParam:
		return "invalid parameter"
	case KindPreHook:
		return "pre-hook rejected"
	case KindPostHook:
		return "post-hook rejected"
	default:
		return "unknown error"
	}
}

// AsImportError 从任意错误中提取归一化错误。
func AsImportError(err error) (*Error, bool) {
	var target *Error
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}

// invalidParam / preHookFailure / postHookFailure 是三个模块共用的错误构造器。
func invalidParam(index int, reason string) error {
	return &Error{Kind: KindInvalidParam, Index: index, Reason: reason}
}

func preHookFailure(index int, reason string) error {
	return &Error{Kind: KindPreHook, Index: index, Reason: reason}
}

func postHookFailure(reason string) error {
	return &Error{Kind: KindPostHook, Index: -1, Reason: reason}
}

// normalize 把钩子返回的任意错误归一化：
// 已经是 *Error 的保留其类别（便于测试钩子直接构造三类错误），
// 普通 error 归入调用方指定的默认类别。
func normalize(index int, defaultKind Kind, err error) error {
	if err == nil {
		return nil
	}
	if e, ok := AsImportError(err); ok {
		return e
	}
	return &Error{Kind: defaultKind, Index: index, Reason: err.Error()}
}
