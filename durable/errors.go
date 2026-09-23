package durable

import "errors"

var (
	// ErrHeaderIncomplete：文件在魔数/版本头部处截断（长度 < 4 字节）。
	ErrHeaderIncomplete = errors.New("durable: header incomplete")
	// ErrValueIncomplete：魔数正确但 8 字节计数值未写完整（长度 < 12）。
	ErrValueIncomplete = errors.New("durable: counter value incomplete")
	// ErrCRCMismatch：值可读但 CRC 缺失或不匹配，文件内容不可信。
	ErrCRCMismatch = errors.New("durable: crc mismatch")
	// ErrWriteFailed：注入的持久写失败；原文件保持不变。
	ErrWriteFailed = errors.New("durable: write failed")
	// ErrOverflow：推进将越过 uint64 上界，拒绝以避免回绕。
	ErrOverflow = errors.New("durable: counter overflow")
	// ErrBadMagic：魔数无法识别（非本系统文件），归入头部不完整类。
	ErrBadMagic = errors.New("durable: bad magic")
)
