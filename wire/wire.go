// Package wire 定义自定义 LZ77 压缩流的字节格式。本包不依赖其他内部包。
package wire

import (
	"errors"
	"hash/crc32"
)

const (
	// Tag 取值，均以 uvarint 编码。
	TagLiteral uint64 = 0
	TagMatch   uint64 = 1
	TagFlush   uint64 = 2
	TagEnd     uint64 = 3

	MinMatchLen = 3
	MaxMatchLen = 1 << 20

	MaxWindow = 1 << 20
	MaxChain  = 1 << 20

	Version = 1
)

// Magic 为流头魔数 "ON"。
var Magic = [2]byte{0x4F, 0x4E}

// ErrVarintTooLong 表示变长整数超过 10 字节或溢出 64 位。
var ErrVarintTooLong = errors.New("wire: varint too long or overflow")

// Config 是写进流头、编解码双方共享的参数。
type Config struct {
	Window     int
	ChainLimit int
}

// AppendUvarint 追加 LEB128 无符号变长整数。
func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint 从 b 头部读取一个 uvarint。
// 返回值、占用字节数；数据不足时 n=0；超长/溢出时 err=ErrVarintTooLong。
func ReadUvarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, ErrVarintTooLong
		}
		v |= uint64(c&0x7F) << (7 * i)
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(b) >= 10 && b[9]&0x80 != 0 {
		return 0, 0, ErrVarintTooLong
	}
	return 0, 0, nil
}

// AppendHeader 写入流头：魔数 + 版本 + 窗口容量 + 链长上限。
func AppendHeader(b []byte, c Config) []byte {
	b = append(b, Magic[:]...)
	b = AppendUvarint(b, Version)
	b = AppendUvarint(b, uint64(c.Window))
	b = AppendUvarint(b, uint64(c.ChainLimit))
	return b
}

// Checksum 返回 IEEE CRC-32 校验和。
func Checksum(data []byte) uint64 {
	return uint64(crc32.ChecksumIEEE(data))
}
