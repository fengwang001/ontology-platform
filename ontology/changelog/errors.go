package changelog

import "errors"

var (
	// ErrEmptyKey 表示写入或读取使用了空键。
	ErrEmptyKey = errors.New("changelog: key must not be empty")
	// ErrPositionOutOfRange 表示读位点不在 [1, NextSeq()-1] 范围内。
	ErrPositionOutOfRange = errors.New("changelog: position out of range")
	// ErrCompactLeftTooSmall 表示压缩区间左边界小于 1。
	ErrCompactLeftTooSmall = errors.New("changelog: compact left bound must be >= 1")
	// ErrCompactRightTooLarge 表示压缩区间右边界超过当前最新序号。
	ErrCompactRightTooLarge = errors.New("changelog: compact right bound must be <= last sequence")
	// ErrCompactInverted 表示压缩区间左边界大于右边界。
	ErrCompactInverted = errors.New("changelog: compact interval must satisfy left <= right")
	// ErrLogCorrupted 表示自检发现日志结构被破坏。
	ErrLogCorrupted = errors.New("changelog: log corrupted")
)
