package zstore

import "errors"

var (
	// ErrPoisoned 表示编解码器在此前出错后进入粘滞失败态，
	// 之后所有调用都返回该错误且不改变任何状态。
	ErrPoisoned = errors.New("zstore: poisoned after previous error")

	// ErrClosed 表示编码器 Close 之后再次 Write 或 Close。
	ErrClosed = errors.New("zstore: encoder already closed")

	// 头部错误，按顺序只报第一个。
	ErrMethod      = errors.New("zstore: unsupported compression method (CM != 8)")
	ErrWindow      = errors.New("zstore: window info too large (CINFO > 7)")
	ErrHeaderCheck = errors.New("zstore: header check failed (not divisible by 31)")
	ErrDict        = errors.New("zstore: preset dictionary flag set")

	// 块级错误。
	ErrBlockHeaderBits      = errors.New("zstore: nonzero high 5 bits in block header")
	ErrBlockTypeUnsupported = errors.New("zstore: unsupported block type (01 or 10)")
	ErrBlockTypeReserved    = errors.New("zstore: reserved block type (11)")
	ErrNLEN                 = errors.New("zstore: NLEN is not the complement of LEN")

	// 尾部错误。
	ErrTrailingData = errors.New("zstore: trailing data after trailer")
	ErrChecksum     = errors.New("zstore: adler-32 checksum mismatch")
	ErrTruncated    = errors.New("zstore: stream truncated before trailer complete")
)
