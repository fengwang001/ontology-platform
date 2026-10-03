// Package regex 实现带本地步数上限、纪元全局预算与封禁升级的回溯正则执行器。
package regex

import "errors"

var (
	// ErrInvalidArgs 参数非法（id 为空或超长、模式为空或超长、输入超长、now 越界、构造参数越界）。
	ErrInvalidArgs = errors.New("regex: invalid arguments")
	// ErrAlreadyExists Register 时 id 已被注册。
	ErrAlreadyExists = errors.New("regex: pattern id already registered")
	// ErrSyntax 模式语法错误。
	ErrSyntax = errors.New("regex: pattern syntax error")
	// ErrNullableRepeat 量词作用于可匹配空串的原子或分组。
	ErrNullableRepeat = errors.New("regex: nullable repeat")
	// ErrProgramTooLarge 编译后的指令总数超过程序大小上限 P。
	ErrProgramTooLarge = errors.New("regex: program too large")
	// ErrNotRegistered 模式未注册。
	ErrNotRegistered = errors.New("regex: pattern not registered")
	// ErrClockRegression now 小于已被接受的 Match 见过的最大 now。
	ErrClockRegression = errors.New("regex: clock regression")
	// ErrBanned 模式处于封禁中。
	ErrBanned = errors.New("regex: pattern banned")
	// ErrGlobalBudgetExhausted 当前纪元全局余量为 0。
	ErrGlobalBudgetExhausted = errors.New("regex: global budget exhausted")
)
