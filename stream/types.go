package stream

import "errors"

var (
	ErrBadByte   = errors.New("stream: illegal byte")
	ErrTruncated = errors.New("stream: truncated input")
	ErrTooLarge  = errors.New("stream: output limit exceeded")
	ErrClosed    = errors.New("stream: transcoder already closed")
)

// BadError 带非法单元在整个输入流中的起始偏移与长度。
type BadError struct{ Offset, Length int64 }

func (e *BadError) Error() string { return "stream: illegal byte" }
func (e *BadError) Unwrap() error { return ErrBadByte }

// TruncError 带截断发生时已彻底消费到的字节偏移。
type TruncError struct{ Offset int64 }

func (e *TruncError) Error() string { return "stream: truncated input" }
func (e *TruncError) Unwrap() error { return ErrTruncated }

// LimitError 带导致超限的单元起始偏移，调用方从该偏移续传。
type LimitError struct{ Offset int64 }

func (e *LimitError) Error() string { return "stream: output limit exceeded" }
func (e *LimitError) Unwrap() error { return ErrTooLarge }
