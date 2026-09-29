package alarm

import "errors"

// 参数与输入校验错误。调用方可用 errors.Is 精确区分失败原因。
var (
	// ErrNonPositiveThreshold 阈值必须为正数。
	ErrNonPositiveThreshold = errors.New("alarm: threshold must be positive")
	// ErrNonPositiveHysteresis 迟滞带必须为正数。
	ErrNonPositiveHysteresis = errors.New("alarm: hysteresis band must be positive")
	// ErrHysteresisTooLarge 迟滞带必须小于阈值（否则解除下沿不大于 0）。
	ErrHysteresisTooLarge = errors.New("alarm: hysteresis band must be smaller than threshold")
	// ErrEmptyKey 键不能为空字符串。
	ErrEmptyKey = errors.New("alarm: key must not be empty")
	// ErrOverflow 增减量会使累计值超出 int64 范围。
	ErrOverflow = errors.New("alarm: cumulative value would overflow int64")
)
