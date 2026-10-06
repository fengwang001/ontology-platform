package maintenance

import "errors"

// 错误哨兵按题目规定的固定优先次序排列：参数非法 < 时钟回退 < 不存在 <
// 状态不允许 < 无候选 < 权限不足。所有校验必须按该次序返回第一个错误。
var (
	ErrInvalidArgument = errors.New("maintenance: invalid argument")
	ErrClockBackward   = errors.New("maintenance: clock moved backwards")
	ErrNotFound        = errors.New("maintenance: work order or contractor not found")
	ErrInvalidState    = errors.New("maintenance: operation not allowed in current state")
	ErrNoCandidate     = errors.New("maintenance: no available candidate and preemption impossible")
	ErrForbidden       = errors.New("maintenance: permission denied")
)
