package ontology

import (
	"errors"
	"fmt"
)

// Reason 是链接操作被拒绝的归一化错误类别。
//
// 创建（含批量导入中的每一条）的拒绝严格按下列优先级只报第一个命中原因：
//  1. ReasonInvalidArgument 参数非法（实例不存在、有序对在本批次内重复等）
//  2. ReasonSourceCardinality 起点一侧基数超限
//  3. ReasonTargetCardinality 终点一侧基数超限
//
// 删除不存在的链接使用独立类别 ReasonLinkNotFound，绝不与基数已满混淆。
type Reason string

const (
	ReasonInvalidArgument   Reason = "invalid_argument"
	ReasonSourceCardinality Reason = "source_cardinality_exceeded"
	ReasonTargetCardinality Reason = "target_cardinality_exceeded"
	ReasonLinkNotFound      Reason = "link_not_found"
)

// LinkError 是所有链接创建/删除/导入失败的统一错误表示。
// 它实现 error；调用方可用 AsLinkError 按 Code 精确区分四类失败。
type LinkError struct {
	Code    Reason
	Message string
}

func (e *LinkError) Error() string {
	return string(e.Code) + ": " + e.Message
}

// AsLinkError 把任意 error 归一化为 *LinkError；非本模块错误返回 nil、false。
func AsLinkError(err error) (*LinkError, bool) {
	if err == nil {
		return nil, false
	}
	var le *LinkError
	if !errors.As(err, &le) {
		return nil, false
	}
	return le, true
}

func invalidArgument(format string, args ...any) *LinkError {
	return &LinkError{Code: ReasonInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

func sourceCapExceeded(format string, args ...any) *LinkError {
	return &LinkError{Code: ReasonSourceCardinality, Message: fmt.Sprintf(format, args...)}
}

func targetCapExceeded(format string, args ...any) *LinkError {
	return &LinkError{Code: ReasonTargetCardinality, Message: fmt.Sprintf(format, args...)}
}

func linkNotFound(format string, args ...any) *LinkError {
	return &LinkError{Code: ReasonLinkNotFound, Message: fmt.Sprintf(format, args...)}
}
