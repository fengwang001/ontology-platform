package activity

import "errors"

// 哨兵错误，可用 errors.Is 区分。
var (
	// ErrConfig：配置取值非法。
	ErrConfig = errors.New("activity: invalid config")
	// ErrArgument：参数非法（空 id、k 越界、now 越界等）。
	ErrArgument = errors.New("activity: invalid argument")
	// ErrClock：now 小于全局时钟。
	ErrClock = errors.New("activity: clock moved backwards")
	// ErrNotFound：活动不存在。
	ErrNotFound = errors.New("activity: not found")
	// ErrStale：k 不等于当前尝试号（含到期重试后迟到的旧尝试结果）。
	ErrStale = errors.New("activity: stale attempt")
	// ErrTerminal：k 相同但活动已终局（含到期当刻的 Complete）。
	ErrTerminal = errors.New("activity: already terminal")
	// ErrState：k 相同但状态不允许该操作（Start 时非 Scheduled 等）。
	ErrState = errors.New("activity: invalid state for operation")
	// ErrExists：Schedule 时 id 已存在。
	ErrExists = errors.New("activity: already exists")
)
