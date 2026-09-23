package stream

import "errors"

// 四类彼此可判定的错误。
var (
	ErrIllegal  = errors.New("stream: illegal byte sequence")
	ErrTruncated = errors.New("stream: truncated input")
	ErrLimit     = errors.New("stream: output limit exceeded")
	ErrTerminal  = errors.New("stream: transcoder already in terminal state")
)

// ByteError 带非法/截断单元在整个输入流中的起始字节偏移与长度。
type ByteError struct {
	Kind   error
	Offset int64
	Len    int
}

func (e *ByteError) Error() string { return e.Kind.Error() }
func (e *ByteError) Unwrap() error { return e.Kind }

// LimitError 是输出超限错误；Head 是未提交的残留前缀（≤3 字节），
// 调用方应把 Head 与未传完的尾部一起喂给新实例续传。
type LimitError struct {
	Head []byte
}

func (e *LimitError) Error() string { return ErrLimit.Error() }
func (e *LimitError) Unwrap() error { return ErrLimit }
