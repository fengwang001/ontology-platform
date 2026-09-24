// Package segment 实现只追加段文件的顺序读写。
// 文件布局：32 字节头 + 若干 [len(4)|body|crc32(4)] 记录。
package segment

import (
	"errors"
)

const (
	magic   = "SLOG"
	version = uint16(1)
	// HeaderSize 是段头固定字节数。
	HeaderSize = 32
)

var (
	// ErrHeaderIncomplete 头不足 HeaderSize 或 magic 不符。
	ErrHeaderIncomplete = errors.New("segment: header incomplete")
	// ErrLenPrefixIncomplete 长度前缀不足 4 字节。
	ErrLenPrefixIncomplete = errors.New("segment: length prefix incomplete")
	// ErrBodyIncomplete 声明的记录体/CRC 超出可读范围。
	ErrBodyIncomplete = errors.New("segment: event body incomplete")
	// ErrCRC CRC32 不匹配。
	ErrCRC = errors.New("segment: crc mismatch")
	// ErrBadHeader 头字段不合法。
	ErrBadHeader = errors.New("segment: bad header")
)

// Header 是段的自描述头。
type Header struct {
	FirstSeq uint64
	Count    uint64
}

// Suffixes 返回段数据文件与索引文件后缀。
func Suffixes() (string, string) { return ".log", ".idx" }
