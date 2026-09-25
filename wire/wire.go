// Package wire 定义 LZ77O 压缩流的字节级格式。
package wire

import (
	"errors"
	"hash/crc32"
)

// Magic 为流头魔数，Version 为当前格式版本。
const (
	Magic   = "LZ77O"
	Version = byte(1)
)

// 记录类型标记。
const (
	TagLiteral = 0 // varint n + n 字节
	TagMatch   = 1 // varint distance + varint length
	TagFlush   = 2 // 无载荷
	TagEnd     = 3 // varint 原文总长 + varint CRC32
)

// 格式相关的哨兵错误。
var (
	// ErrHeader 流头魔数或版本错误。
	ErrHeader = errors.New("wire: bad magic or version")
	// ErrVarint 变长整数超过 10 字节或溢出 64 位。
	ErrVarint = errors.New("wire: varint too long or overflow")
	// ErrTag 出现未知记录标记。
	ErrTag = errors.New("wire: unknown record tag")
	// ErrTruncated 字节流在记录中途结束。
	ErrTruncated = errors.New("wire: truncated stream")
)

// Header 返回流头字节。
func Header() []byte { return []byte(Magic + string(Version)) }

// AppendUvarint 追加无符号 LEB128 变长整数。
func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint 从 b 读取一个变长整数，返回值与消耗字节数；
// 数据不足返回 ErrTruncated，超长/溢出返回 ErrVarint。
func ReadUvarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b); i++ {
		c := b[i]
		if i == 10 {
			return 0, 0, ErrVarint
		}
		if i == 9 && (c > 1 || c&0x80 != 0) {
			return 0, 0, ErrVarint
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, ErrTruncated
}

// Checksum 返回 CRC32-IEEE 校验和。
func Checksum(p []byte) uint64 { return uint64(crc32.ChecksumIEEE(p)) }
