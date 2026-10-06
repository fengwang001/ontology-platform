package ontology

import "errors"

// 固定错误次序：参数非法 < 时钟回退 < 不存在 < 在住期重叠 < 状态不允许 < 金额越界。
var (
	ErrInvalid   = errors.New("invalid argument")
	ErrClockBack = errors.New("clock moved backwards")
	ErrNotFound  = errors.New("resident or bill not found")
	ErrOverlap   = errors.New("occupancy periods overlap")
	ErrState     = errors.New("operation not allowed in current state")
	ErrAmount    = errors.New("amount out of range")
)
