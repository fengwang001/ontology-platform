// Package handler 在 history 与 dedupe 之上实现“先校验、后入史”的
// 工作流更新状态机，支持 Close 与凭历史重放的 Recover。
package handler

import "errors"

// 哨兵错误：用 errors.Is 判定，均不写历史、不登记去重表。
var (
	ErrInvalidParam = errors.New("handler: invalid parameter")
	ErrNotFound     = errors.New("handler: instance not found")
	ErrExists       = errors.New("handler: instance already exists")
	ErrClosed       = errors.New("handler: instance closed")
	ErrEmpty        = errors.New("handler: pending queue empty")
)

// 参数边界。
const (
	MinDelta   = -1_000_000_000_000
	MaxDelta   = 1_000_000_000_000
	MaxCap     = 1_000_000_000_000
	MinDedupeK = 1
	MaxDedupeK = 1_000_000
)

// Kind 标识 uid 的登记结果种类。
type Kind uint8

const (
	Unknown   Kind = iota // 去重表中没有
	Rejected              // 校验未通过（正常结果，非错误）
	Accepted              // 已入史入队，携带 U 序号
	Completed             // 已应用，携带应用后的新值
	Aborted               // 实例关闭时仍在队列中
)

// Result 是某 uid 的登记结果。Unknown 时其他字段为零值。
type Result struct {
	Kind Kind
	Seq  int64 // Accepted：U 事件序号
	Val  int64 // Completed：应用后的值
}

func (r Result) equal(o Result) bool {
	return r.Kind == o.Kind && r.Seq == o.Seq && r.Val == o.Val
}
