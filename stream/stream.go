// Package stream 实现带非法字节替换的流式 UTF-8 ⇄ UTF-16 转码。
// 单个 Transcoder 实例不是并发安全的；并行请用 par 包。
package stream

import (
	"errors"
)

// Format 是输入/输出的编码格式。
type Format uint8

const (
	UTF8 Format = iota
	UTF16LE
	UTF16BE
)

// 四类彼此可判定的错误。
var (
	// ErrIllegal：非法字节单元。用 errors.As 解出 *UnitError 取偏移与长度。
	ErrIllegal = errors.New("stream: illegal byte unit")
	// ErrTruncated：流结束时合法前缀被截断。
	ErrTruncated = errors.New("stream: truncated input")
	ErrLimit     = errors.New("stream: output size limit exceeded")
	ErrClosed    = errors.New("stream: transcoder is in terminal state")
)

// UnitError 携带非法/截断单元在整个输入流中的起始偏移与长度。
type UnitError struct {
	Err    error
	Offset int64
	Size   int
}

func (e *UnitError) Error() string { return e.Err.Error() }
func (e *UnitError) Unwrap() error { return e.Err }

// Stats 是字节守恒统计。
type Stats struct {
	Scalars      int64 // 合法标量数（不含被丢弃的 BOM）
	Illegal      int64 // 非法单元数
	IllegalBytes int64 // 非法单元吞掉的字节数
	BOMBytes     int64 // BOM 占用字节数
	Consumed     int64 // 已消费输入字节总数
	Checked      int64 // 字节被检查总次数
}

// Config 配置转码器。
type Config struct {
	From, To Format
	Strict   bool // true：首个非法单元即报错并进入终态
	EmitBOM  bool // 输出开头是否写 BOM；输入开头 BOM 始终被识别
	MaxOut   int  // 输出字节上限；≤0 表示不限
	// OffsetBase 是本段首字节在更大输入中的全局偏移（par 内部使用）。
	OffsetBase int64
	// MidStream 表示本段不从流开头开始：不做 BOM 识别（par 内部使用）。
	MidStream bool
}
