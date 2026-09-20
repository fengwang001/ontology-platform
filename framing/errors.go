package framing

import "errors"

var (
	// ErrFrameTooLarge 表示长度字段超过 New 设定的上限。
	// 一旦出现，读取器进入终止态，后续 Feed 一律返回该错误。
	ErrFrameTooLarge = errors.New("framing: frame length exceeds maximum")

	// ErrIncomplete 表示 Close 时缓冲区仍有未成帧的残留字节。
	ErrIncomplete = errors.New("framing: stream ended with an incomplete frame")

	// ErrClosed 表示在 Close 之后继续 Feed。
	ErrClosed = errors.New("framing: feed after close")
)
