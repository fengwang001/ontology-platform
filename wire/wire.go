// Package wire 定义自定 LZ77 压缩流的字节格式。本包不依赖其他包。
package wire

import (
	"encoding/binary"
	"errors"
)

const (
	// Magic 为流头魔数 "ONTL"。
	Magic = "ONTL"
	// Version 是当前流格式版本。
	Version = uint64(1)

	TagLiteral byte = 0 // 负载：varint 长度 + 原文字节
	TagMatch   byte = 1 // 负载：varint 距离(>=1) + varint 长度(>=MinMatch)
	TagFlush   byte = 2 // 无负载
	TagEnd     byte = 3 // 负载：varint 原文总长 + 4 字节 CRC32 大端

	MinMatch = 3
)

var (
	// ErrTruncated 表示记录在缓冲区中被截断（非损坏类，可等更多字节）。
	ErrTruncated = errors.New("wire: truncated record")
	// ErrVarintOverflow 表示变长整数超过 10 字节或溢出 64 位。
	ErrVarintOverflow = errors.New("wire: varint overflows 64-bit integer")
)

// AppendUvarint 追加一个 base128 little-endian 变长整数。
func AppendUvarint(b []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(b, buf[:n]...)
}

// ReadUvarint 从 buf 起点读取一个变长整数。
// 返回值、占用字节数；缓冲不足返回 ErrTruncated，超长/溢出返回 ErrVarintOverflow。
func ReadUvarint(buf []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < binary.MaxVarintLen64; i++ {
		if i >= len(buf) {
			return 0, i, ErrTruncated
		}
		c := buf[i]
		if i == binary.MaxVarintLen64-1 {
			// 第 10 字节只允许最低位，且不得再有续位。
			if c > 1 {
				return 0, i + 1, ErrVarintOverflow
			}
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, i + 1, nil
		}
	}
	return 0, binary.MaxVarintLen64, ErrVarintOverflow
}

// AppendHeader 追加流头：魔数 + 版本 + 窗口容量 + 链长上限。
func AppendHeader(b []byte, windowSize, chainLimit int) []byte {
	b = append(b, Magic...)
	b = AppendUvarint(b, Version)
	b = AppendUvarint(b, uint64(windowSize))
	b = AppendUvarint(b, uint64(chainLimit))
	return b
}
