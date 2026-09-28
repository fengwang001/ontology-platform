package barrier

import "errors"

var (
	// ErrInvalidChannel 通道号不是 0 或 1。
	ErrInvalidChannel = errors.New("barrier: invalid channel id")
	// ErrEmptyKey 普通记录的 Key 为空。
	ErrEmptyKey = errors.New("barrier: empty record key")
	// ErrInvalidBarrierNo 屏障编号不等于该通道期望的下一个编号。
	ErrInvalidBarrierNo = errors.New("barrier: barrier number out of order")
	// ErrBufferLimitExceeded 缓冲的普通记录数超过配置上限。
	ErrBufferLimitExceeded = errors.New("barrier: buffered record limit exceeded")
)
