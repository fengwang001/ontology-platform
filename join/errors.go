// Package join 实现两张多重集表（multiset）的等值内连接增量差分维护。
package join

import (
	"errors"
	"strconv"
)

// RejectReason 标识一个批次被拒绝的具体原因，保证不同非法情形可区分。
type RejectReason int

const (
	// ReasonOK 表示批次通过校验并已提交。
	ReasonOK RejectReason = iota
	// ReasonNullValue 空值：连接键或负载值为空（NULL）。
	ReasonNullValue
	// ReasonInvalidDelta 变更符号非法：行重数变更量为 0。
	ReasonInvalidDelta
	// ReasonDeleteNonexistent 删除不存在的行：批后某行出现负重数。
	ReasonDeleteNonexistent
	// ReasonResultTooLarge 结果元组数超限。
	ReasonResultTooLarge
	// ReasonMultiplicityOverflow 连接重数乘积或累计发生 int64 溢出。
	ReasonMultiplicityOverflow
)

// String 返回原因的稳定短名，用于日志与测试断言。
func (r RejectReason) String() string {
	switch r {
	case ReasonOK:
		return "ok"
	case ReasonNullValue:
		return "null_value"
	case ReasonInvalidDelta:
		return "invalid_delta"
	case ReasonDeleteNonexistent:
		return "delete_nonexistent"
	case ReasonResultTooLarge:
		return "result_too_large"
	case ReasonMultiplicityOverflow:
		return "multiplicity_overflow"
	default:
		return "unknown"
	}
}

// RejectError 描述被拒绝批次的结构化原因，供 errors.Is/As 判定与日志记录。
//
// 一个 RejectError 既承载细粒度的 Reason，也可与同 Reason 的哨兵错误用 errors.Is 匹配：
//
//	var e *join.RejectError
//	errors.As(res.Err, &e)                       // 取结构化信息
//	errors.Is(res.Err, join.ErrDeleteNonexistent) // 按原因判定
type RejectError struct {
	Reason RejectReason
	// Side 为 "left" 或 "right"；仅结果级错误（如结果超限）为空。
	Side string
	// Key/Value 指出触发行（存在时填写）。
	Key    string
	Value  string
	Detail string
}

func (e *RejectError) Error() string {
	if e == nil {
		return "join: <nil>"
	}
	s := "join: batch rejected: " + e.Reason.String()
	if e.Side != "" {
		s += " on " + e.Side
	}
	if e.Key != "" {
		s += " key=" + quote(e.Key)
	}
	if e.Value != "" {
		s += " value=" + quote(e.Value)
	}
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

// Is 支持与同 Reason 的哨兵错误做 errors.Is 比较。
func (e *RejectError) Is(target error) bool {
	t, ok := target.(*RejectError)
	if !ok {
		return false
	}
	return e.Reason == t.Reason
}

// 哨兵错误：每种可区分原因各一个，供 errors.Is 判定。
var (
	ErrNullValue            = &RejectError{Reason: ReasonNullValue}
	ErrInvalidDelta         = &RejectError{Reason: ReasonInvalidDelta}
	ErrDeleteNonexistent    = &RejectError{Reason: ReasonDeleteNonexistent}
	ErrResultTooLarge       = &RejectError{Reason: ReasonResultTooLarge}
	ErrMultiplicityOverflow = &RejectError{Reason: ReasonMultiplicityOverflow}
)

// reject 构造结构化拒绝错误。
func reject(reason RejectReason, side, key, value, detail string) *RejectError {
	return &RejectError{
		Reason: reason,
		Side:   side,
		Key:    key,
		Value:  value,
		Detail: detail,
	}
}

// AsReject 从任意 error 中提取 *RejectError（便捷封装）。
func AsReject(err error) (*RejectError, bool) {
	var e *RejectError
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// quote 用 strconv.Quote 对字符串做可区分空串/特殊字符的转义表示。
func quote(s string) string {
	return strconv.Quote(s)
}
