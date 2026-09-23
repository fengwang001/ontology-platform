package segment

import "errors"

// 四类可被 errors.Is 区分的段错误。
var (
	// ErrHeader 头部（magic/dataLen/dictLen）不完整或魔数错误。
	ErrHeader = errors.New("segment: header incomplete or bad magic")
	// ErrDict 词典字节不完整，无法解析出全部词典项。
	ErrDict    = errors.New("segment: dictionary incomplete")
	// ErrPosting 倒排链字节不完整或链自身 CRC 不符。
	ErrPosting = errors.New("segment: posting chain incomplete")
	// ErrCRC 整段 CRC32 不匹配（文件完整但内容被改/截断恰好到达长度）。
	ErrCRC = errors.New("segment: crc mismatch")
)

func wrap(base error, msg string) error {
	return &wrapError{base: base, msg: msg}
}

type wrapError struct {
	base error
	msg  string
}

func (e *wrapError) Error() string { return e.msg + ": " + e.base.Error() }
func (e *wrapError) Unwrap() error { return e.base }
