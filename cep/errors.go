package cep

import "errors"

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	// ErrInvalidConfig 表示匹配器参数非法（空类型、非正窗口、非正队列上限等）。
	ErrInvalidConfig = errors.New("cep: invalid config")
	// ErrEmptyKey 表示事件键为空。
	ErrEmptyKey = errors.New("cep: empty event key")
	// ErrEmptyType 表示事件类型为空。
	ErrEmptyType = errors.New("cep: empty event type")
	// ErrTimeRegression 表示同键事件时间出现倒退。
	ErrTimeRegression = errors.New("cep: timestamp regression")
	// ErrPendingOverflow 表示单键待匹配队列超出上限。
	ErrPendingOverflow = errors.New("cep: pending queue overflow")
)
